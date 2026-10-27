<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  BookOpen,
  ChevronLeft,
  ChevronRight,
  CircleCheck,
  Download,
  EyeOff,
  ExternalLink,
  Languages,
  Loader2,
  Merge,
  Rss,
  Search,
  Tag,
  UserRound,
  Square,
  SquareCheck,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import timeSortAsc from '../assets/icons/time-sort-asc.png'
import timeSortDesc from '../assets/icons/time-sort-desc.png'
import BackArrowIcon from '../components/BackArrowIcon.vue'
import DirectoryPicker from '../components/DirectoryPicker.vue'
import FunctionOption from '../components/FunctionOption.vue'
import { notify } from '../stores/notices'
import { comicPreview } from '../stores/comicPreview'
import { lastSubscriptionDownloadDir, rememberSubscriptionDownloadDir } from '../stores/subscriptionPreferences'
import { splitAuthors } from '../utils/metadata'
import { paginateTextPages } from '../utils/reader'

const route = useRoute()
const router = useRouter()
const sourceId = route.params.sourceId
const comicId = route.params.comicId
const isBook = ref(false)

const detail = ref(null)
const selected = ref(new Set())
const chapterSelectionMode = ref(false)
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const autoDownload = ref(true)
const sortDesc = ref(false)
const queryInput = ref('')
const appliedQuery = ref('')
const heroTint = ref('')
const downloadedChapters = ref(new Set())
const previewOpen = ref(false)
const previewLoading = ref(false)
const previewError = ref('')
const previewKind = ref('')
const previewTitle = ref('')
const previewText = ref('')
const previewPages = ref([])
const previewPageIndex = ref(0)
// 从订阅清单的“归档集”进入时，章节下面要标出哪些章节有过下载记录。
const archiveMode = computed(() => String(route.query.archive || '') === '1')

const chapterRenderLimit = ref(80)
let pageScroller = null
let anchorIndex = -1
let previewRequestSeq = 0

const subscribeOpen = ref(false)
const downloadOpen = ref(false)
const subForm = ref({
  cronExpr: '',
  downloadDir: lastSubscriptionDownloadDir(),
  seriesDir: '',
  convertToSimplified: true,
  chapterMarker: false,
  coverBlur: false,
  allChapters: true,
})
const downloadForm = ref({
  downloadDir: lastSubscriptionDownloadDir(),
  seriesDir: '',
  convertToSimplified: true,
  chapterMarker: false,
  mergeChapters: false,
})

const existingSubscription = ref(null)
const sourceAccounts = ref({})
const sourceDefaultDownloadDir = computed(() => {
  const account = sourceAccounts.value[sourceId]
  return String(account?.settings?.defaultDownloadDir || '').trim()
})
const sourceDefaultCoverBlur = computed(() => Boolean(sourceAccounts.value[sourceId]?.settings?.coverBlur))
const isSubscribed = computed(() => Boolean(existingSubscription.value))
const isBangumiComic = computed(() => String(sourceId || '').toLowerCase().startsWith('bangumi-'))
const comic = computed(() => detail.value?.comic || null)
const comicAuthors = computed(() => splitAuthors(comic.value?.author))
const sourcePageUrl = computed(() => String(comic.value?.sourceUrl || '').trim())
const lastUpdated = computed(() => String(comic.value?.updatedAt || '').trim())
// 详情页只显示到年月日：2026-10-18。
const updatedDateLabel = computed(() => {
  const raw = lastUpdated.value
  if (!raw) return ''
  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (value) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
})
const chapters = computed(() => detail.value?.chapters || [])
const previewTextPages = computed(() => paginateTextPages(previewText.value))
const previewPageCount = computed(() => {
  if (previewKind.value === 'text') return previewTextPages.value.length
  return previewPages.value.length
})
const previewCurrentImage = computed(() => previewPages.value[previewPageIndex.value] || '')
const previewCurrentText = computed(() => previewTextPages.value[previewPageIndex.value] || '')
const previewVideoUrl = computed(() => previewPages.value[previewPageIndex.value] || '')
const displayChapters = computed(() => {
  const keyword = appliedQuery.value.trim().toLowerCase()
  let list = chapters.value
  if (keyword) {
    list = list.filter((chapter) => String(chapter.title || '').toLowerCase().includes(keyword))
  }
  const rows = [...list]
  rows.sort((a, b) => (sortDesc.value ? (b.order || 0) - (a.order || 0) : (a.order || 0) - (b.order || 0)))
  return rows
})
const renderedChapters = computed(() => displayChapters.value.slice(0, chapterRenderLimit.value))
const allSelected = computed(
  () => displayChapters.value.length > 0 && displayChapters.value.every((chapter) => selected.value.has(chapter.id)),
)
const downloadedCount = computed(
  () => chapters.value.filter((chapter) => downloadedChapters.value.has(chapter.id)).length,
)
const heroStyle = computed(() => ({
  '--hero-tint': heroTint.value || 'transparent',
  '--hero-cover': comic.value?.cover ? `url("${cover()}")` : 'none',
}))

watch([appliedQuery, sortDesc], () => {
  chapterRenderLimit.value = 80
})

function handlePageScroll(event) {
  const element = event.currentTarget
  if (!element || chapterRenderLimit.value >= displayChapters.value.length) return
  if (element.scrollTop + element.clientHeight >= element.scrollHeight - 280) {
    chapterRenderLimit.value += 80
  }
}

function handleReaderKeydown(event) {
  if (previewOpen.value) {
    if (event.key === 'Escape') {
      closeChapterPreview()
      return
    }
    const tagName = String(event.target?.tagName || '').toUpperCase()
    if (tagName === 'VIDEO' || tagName === 'AUDIO' || ['INPUT', 'TEXTAREA', 'SELECT'].includes(tagName)) return
    if (event.key === 'ArrowLeft') {
      event.preventDefault()
      stepPreviewPage(-1)
    } else if (event.key === 'ArrowRight') {
      event.preventDefault()
      stepPreviewPage(1)
    }
    return
  }
  if (event.key === 'Escape' && chapterSelectionMode.value) clearChapterSelection()
}

function defaultCron() {
  const now = new Date()
  return `${now.getMinutes()} ${now.getHours()} * * ${now.getDay()}`
}

onMounted(async () => {
  // 从探索发现点进来时先用列表卡片数据渲染首屏，详情请求回来再覆盖。
  const preview = comicPreview(sourceId, comicId)
  if (preview) {
    detail.value = { comic: { ...preview }, chapters: [] }
    loading.value = false
  }
  try {
    detail.value = await api.comic(sourceId, comicId)
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
  try {
    const resolvedComicId = comic.value?.id || comicId
    const [sourcePayload, subscriptionPayload] = await Promise.all([
      api.sources().catch(() => ({ items: [] })),
      api.matchSubscription(sourceId, resolvedComicId).catch(() => ({ item: null, latestDownloadDir: '' })),
    ])
    const source = (sourcePayload.items || []).find((item) => item.id === sourceId)
    sourceAccounts.value = sourcePayload.accounts || {}
    isBook.value = source?.kind === 'book'
    let matchedSubscription = subscriptionPayload.item || null
    if (!matchedSubscription && String(resolvedComicId) !== String(comicId)) {
      matchedSubscription = await api
        .matchSubscription(sourceId, comicId)
        .then((payload) => payload.item || null)
        .catch(() => null)
    }
    existingSubscription.value = matchedSubscription
    if (sourceDefaultDownloadDir.value) {
      // 该内容源设了默认下载位置时优先使用它。
      subForm.value.downloadDir = sourceDefaultDownloadDir.value
      downloadForm.value.downloadDir = sourceDefaultDownloadDir.value
    } else if (!lastSubscriptionDownloadDir() && subscriptionPayload.latestDownloadDir) {
      const latestDownloadDir = subscriptionPayload.latestDownloadDir
      subForm.value.downloadDir = latestDownloadDir
      rememberSubscriptionDownloadDir(latestDownloadDir)
    }
  } catch {
    isBook.value = false
  }
  if (archiveMode.value) {
    try {
      const payload = await api.comicDownloads(sourceId, comicId)
      downloadedChapters.value = new Set(payload.chapterIds || [])
    } catch {
      downloadedChapters.value = new Set()
    }
  }
  if (comic.value?.cover) extractTint(cover())
  pageScroller = document.querySelector('.content-scroll')
  pageScroller?.addEventListener('scroll', handlePageScroll, { passive: true })
  window.addEventListener('keydown', handleReaderKeydown)
})

onUnmounted(() => {
  pageScroller?.removeEventListener('scroll', handlePageScroll)
  window.removeEventListener('keydown', handleReaderKeydown)
  previewRequestSeq += 1
})

function extractTint(url) {
  try {
    const image = new Image()
    image.crossOrigin = 'anonymous'
    image.onload = () => {
      try {
        const canvas = document.createElement('canvas')
        canvas.width = 20
        canvas.height = 30
        const context = canvas.getContext('2d')
        context.drawImage(image, 0, 0, 20, 30)
        const { data } = context.getImageData(0, 0, 20, 30)
        let r = 0
        let g = 0
        let b = 0
        let count = 0
        for (let i = 0; i < data.length; i += 4) {
          r += data[i]
          g += data[i + 1]
          b += data[i + 2]
          count += 1
        }
        if (count) {
          heroTint.value = `rgb(${Math.round(r / count)}, ${Math.round(g / count)}, ${Math.round(b / count)})`
        }
      } catch {
        heroTint.value = ''
      }
    }
    image.src = url
  } catch {
    heroTint.value = ''
  }
}

function goBack() {
  if (window.history.state?.back) {
    router.back()
  } else {
    router.push({ name: 'discover' })
  }
}

function searchAuthor(name) {
  const author = String(name || comic.value?.author || '').trim()
  if (!author) return
  router.push({ name: 'search', query: { q: author, kind: isBook.value ? 'book' : 'comic' } })
}

function handleChapterClick(event, chapter, index) {
  if (!chapterSelectionMode.value || event.detail > 1) return
  const next = new Set(selected.value)
  if (event.shiftKey && anchorIndex >= 0) {
    const [start, end] = anchorIndex < index ? [anchorIndex, index] : [index, anchorIndex]
    for (let i = start; i <= end; i += 1) {
      const row = displayChapters.value[i]
      if (row) next.add(row.id)
    }
  } else if (next.has(chapter.id)) {
    next.delete(chapter.id)
    anchorIndex = index
  } else {
    next.add(chapter.id)
    anchorIndex = index
  }
  selected.value = next
  if (!next.size) chapterSelectionMode.value = false
}

function toggleAll() {
  if (!chapterSelectionMode.value) {
    chapterSelectionMode.value = true
    selected.value = new Set(displayChapters.value.map((item) => item.id))
  } else if (allSelected.value) {
    clearChapterSelection()
  } else {
    selected.value = new Set([...selected.value, ...displayChapters.value.map((item) => item.id)])
  }
}

function enterChapterSelection(event, chapter, index) {
  event.preventDefault()
  chapterSelectionMode.value = true
  anchorIndex = index
  if (!selected.value.has(chapter.id)) {
    selected.value = new Set([...selected.value, chapter.id])
  }
}

function clearChapterSelection() {
  chapterSelectionMode.value = false
  selected.value = new Set()
  anchorIndex = -1
}

function previewPlainText(value) {
  const raw = String(value || '').trim()
  if (!raw) return ''
  const document = new DOMParser().parseFromString(raw, 'text/html')
  document.querySelectorAll('br').forEach((node) => node.replaceWith('\n'))
  document.querySelectorAll('p, div, section, article, li, h1, h2, h3, h4, h5, h6').forEach((node) => {
    node.append('\n')
  })
  return String(document.body?.textContent || raw)
    .replace(/\r/g, '')
    .replace(/[ \t]+\n/g, '\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}

async function openChapterPreview(chapter) {
  if (!chapter?.id || !comic.value) return
  const requestSeq = ++previewRequestSeq
  previewOpen.value = true
  previewLoading.value = true
  previewError.value = ''
  previewKind.value = ''
  previewTitle.value = chapter.title || chapter.id
  previewText.value = ''
  previewPages.value = []
  previewPageIndex.value = 0
  try {
    const payload = await api.chapterPreview(sourceId, comic.value.id, chapter)
    if (requestSeq !== previewRequestSeq) return
    previewKind.value = payload.kind || ''
    previewTitle.value = payload.title || previewTitle.value
    if (payload.kind === 'images') {
      previewPages.value = Array.isArray(payload.pages) ? payload.pages : []
    } else if (payload.kind === 'video') {
      const pageCount = Math.max(0, Number(payload.pageCount || 0))
      previewPages.value = Array.from({ length: pageCount }, (_, index) =>
        api.chapterPreviewFileUrl(sourceId, comic.value.id, payload.key, index),
      ).filter(Boolean)
    } else {
      previewText.value = previewPlainText(payload.content)
    }
  } catch (err) {
    if (requestSeq === previewRequestSeq) previewError.value = err.message
  } finally {
    if (requestSeq === previewRequestSeq) previewLoading.value = false
  }
}

function closeChapterPreview() {
  previewRequestSeq += 1
  previewOpen.value = false
  previewLoading.value = false
  previewError.value = ''
  previewKind.value = ''
  previewText.value = ''
  previewPages.value = []
  previewPageIndex.value = 0
}

function stepPreviewPage(delta) {
  const count = previewPageCount.value
  if (count <= 1) return
  previewPageIndex.value = Math.min(count - 1, Math.max(0, previewPageIndex.value + delta))
}

function applyChapterSearch() {
  appliedQuery.value = queryInput.value
}

function clearChapterSearch() {
  queryInput.value = ''
  appliedQuery.value = ''
}

function cover() {
  return api.imageUrl(comic.value?.cover, sourceId)
}

function openSubscribe() {
  if (isSubscribed.value) {
    notify('该作品已在订阅清单中', 'info')
    return
  }
  error.value = ''
  subForm.value = {
    cronExpr: defaultCron(),
    downloadDir: sourceDefaultDownloadDir.value || subForm.value.downloadDir || lastSubscriptionDownloadDir(),
    seriesDir: '',
    convertToSimplified: true,
    chapterMarker: false,
    coverBlur: sourceDefaultCoverBlur.value,
    allChapters: true,
  }
  // 自动追更默认勾选；关掉后 Cron 留空，等于不启用定时检查。
  autoDownload.value = true
  subscribeOpen.value = true
}

function toggleAutoUpdate() {
  autoDownload.value = !autoDownload.value
  if (autoDownload.value) {
    if (!subForm.value.cronExpr.trim()) subForm.value.cronExpr = defaultCron()
  } else {
    subForm.value.cronExpr = ''
  }
}

async function confirmSubscribe() {
  if (!comic.value) return
  if (isSubscribed.value) {
    notify('该作品已在订阅清单中', 'info')
    return
  }
  if (autoDownload.value && !subForm.value.cronExpr.trim()) {
    notify('自动追更需要填写 Cron 表达式', 'warning')
    return
  }
  if (!subForm.value.downloadDir.trim()) {
    notify('请选择下载位置', 'warning')
    return
  }
  busy.value = true
  error.value = ''
  try {
    const last = chapters.value[chapters.value.length - 1]
    const result = await api.createSubscription({
      sourceId,
      comicId: comic.value.id,
      title: comic.value.title,
      cover: comic.value.cover,
      author: comic.value.author,
      sourceUrl: comic.value.sourceUrl,
      autoDownload: autoDownload.value,
      enabled: true,
      baseline: subForm.value.allChapters ? '' : last?.id || '',
      lastChapterOrder: subForm.value.allChapters ? 0 : last?.order || 0,
      allChapters: subForm.value.allChapters,
      cronExpr: autoDownload.value ? subForm.value.cronExpr.trim() : '',
      downloadDir: subForm.value.downloadDir.trim(),
      seriesDir: subForm.value.seriesDir.trim(),
      convertToSimplified: Boolean(subForm.value.convertToSimplified),
      chapterMarker: Boolean(subForm.value.chapterMarker),
      coverBlur: Boolean(subForm.value.coverBlur),
    })
    subscribeOpen.value = false
    rememberSubscriptionDownloadDir(subForm.value.downloadDir)
    existingSubscription.value = result?.item || { sourceId, comicId: comic.value.id }
    const queued = Number(result?.queued || 0)
    notify(
      subForm.value.allChapters
        ? `订阅已创建，${queued ? `全量下载已加入 ${queued} 个章节` : '全量下载已完成'}`
        : '订阅已创建，新章节会按检查计划自动进入下载队列',
      'success',
    )
  } catch (err) {
    error.value = err.message
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

function openDownloadModal() {
  if (!selected.value.size) return
  downloadForm.value = {
    downloadDir:
      existingSubscription.value?.downloadDir ||
      subForm.value.downloadDir ||
      lastSubscriptionDownloadDir(),
    seriesDir: existingSubscription.value?.seriesDir || '',
    convertToSimplified: existingSubscription.value?.convertToSimplified ?? true,
    chapterMarker: existingSubscription.value?.chapterMarker ?? false,
    mergeChapters: false,
  }
  downloadOpen.value = true
}

async function downloadSelected() {
  const picked = chapters.value.filter((item) => selected.value.has(item.id))
  if (!picked.length || !comic.value) return
  if (!downloadForm.value.downloadDir.trim()) {
    notify('请选择下载位置', 'warning')
    return
  }
  busy.value = true
  error.value = ''
  try {
    const result = await api.createDownload({
      sourceId,
      comicId: comic.value.id,
      comicTitle: comic.value.title,
      comicCover: comic.value.cover,
      comicAuthor: comic.value.author,
      comicDescription: comic.value.description,
      comicTags: comic.value.tags || [],
      // 只下载，不订阅：订阅清单只应该收录显式订阅过的作品。
      autoDownload: false,
      mergeChapters: Boolean(downloadForm.value.mergeChapters),
      chapters: picked,
      downloadDir: downloadForm.value.downloadDir.trim(),
      seriesDir: downloadForm.value.seriesDir.trim(),
      convertToSimplified: Boolean(downloadForm.value.convertToSimplified),
      chapterMarker: Boolean(downloadForm.value.chapterMarker),
    })
    downloadOpen.value = false
    rememberSubscriptionDownloadDir(downloadForm.value.downloadDir)
    const queued = Number(result.queued || 0)
    const skipped = Number(result.skipped || 0)
    notify(
      [`已加入 ${queued} 个章节`, skipped ? `跳过 ${skipped} 个已下载章节` : ''].filter(Boolean).join('，'),
      'success',
    )
    clearChapterSelection()
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div v-if="loading" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载作品信息</span>
  </div>
  <div v-else-if="!comic" class="card empty">
    <BookOpen :size="26" />
    <p>{{ error || '没有找到该作品' }}</p>
  </div>
  <div v-else>
    <div class="detail-hero" :style="heroStyle">
      <button class="detail-cover-back" type="button" aria-label="返回" @click="goBack">
        <BackArrowIcon />
      </button>
      <div class="detail-cover-wrap">
        <div class="detail-cover">
        <img v-if="comic.cover" :src="cover()" :alt="comic.title" />
        <span v-else class="cover-fallback"><BookOpen :size="28" /></span>
        </div>
        <a
          v-if="sourcePageUrl"
          class="detail-source-link"
          :href="sourcePageUrl"
          target="_blank"
          rel="noopener noreferrer"
        >
          <ExternalLink :size="13" />
          <span>{{ sourcePageUrl }}</span>
        </a>
      </div>
      <div class="detail-info">
        <div class="detail-info-head">
          <h2>{{ comic.title }}</h2>
          <div class="detail-actions">
            <button
              v-if="!isBangumiComic"
              class="btn download-btn"
              type="button"
              :disabled="busy || !selected.size"
              @click="openDownloadModal"
            >
              <Loader2 v-if="busy" :size="16" class="spin" />
              <Download v-else :size="16" />
              下载 ({{ selected.size }})
            </button>
            <button
              class="btn subscribe-btn"
              :class="{ 'is-subscribed': isSubscribed }"
              type="button"
              :disabled="busy"
              :aria-label="isSubscribed ? '已订阅' : '订阅'"
              @click="openSubscribe"
            >
              <CircleCheck v-if="isSubscribed" :size="16" />
              <Rss v-else :size="16" />
              {{ isSubscribed ? '已订阅' : '订阅' }}
            </button>
          </div>
        </div>
        <div class="meta detail-meta-line">
          <template v-if="comicAuthors.length">
            <button
              v-for="name in comicAuthors"
              :key="name"
              class="author-pill"
              type="button"
              @click="searchAuthor(name)"
            >
              <UserRound :size="12" :stroke-width="1.9" />
              {{ name }}
            </button>
          </template>
          <span v-else>未知作者</span>
          <span v-if="comic.status"> · {{ comic.status }}</span>
          <span v-if="updatedDateLabel"> · 最后更新 {{ updatedDateLabel }}</span>
        </div>
        <div v-if="comic.tags?.length" class="tag-list">
          <span v-for="tag in comic.tags" :key="tag" class="badge tag-pill">
            <Tag :size="12" />{{ tag }}
          </span>
        </div>
        <p class="description">{{ comic.description || '暂无简介' }}</p>
      </div>
    </div>

    <section v-if="!isBangumiComic" class="detail-chapters">
      <div class="detail-chapter-head">
        <button
          class="btn ghost small select-all-btn"
          :class="{ active: chapterSelectionMode }"
          type="button"
          @click="toggleAll"
        >
          <component :is="allSelected ? SquareCheck : Square" :size="16" />
          <span>全选</span>
        </button>
        <button class="btn ghost small sort-btn" type="button" :aria-label="sortDesc ? '最新在前' : '最旧在前'" @click="sortDesc = !sortDesc">
          <img class="chapter-time-sort-icon" :src="sortDesc ? timeSortAsc : timeSortDesc" alt="" />
        </button>
        <span v-if="archiveMode" class="muted small">已下载 {{ downloadedCount }} / 共 {{ chapters.length }} 章</span>
        <span v-else class="muted small">
          共 {{ chapters.length }} 章
          <template v-if="updatedDateLabel"> · 最后更新：{{ updatedDateLabel }}</template>
        </span>
        <span class="spacer" />
        <form class="chapter-search" @submit.prevent="applyChapterSearch">
          <Search :size="15" />
          <input v-model="queryInput" class="input" placeholder="搜索章节" @keyup.enter="applyChapterSearch" />
          <button v-if="queryInput" class="icon-btn" type="button" aria-label="清除" @click="clearChapterSearch">
            <X :size="14" />
          </button>
        </form>
      </div>

      <div v-if="!displayChapters.length" class="empty">没有匹配的章节</div>
      <div v-else class="chapter-grid" :class="{ selecting: chapterSelectionMode }">
        <button
          v-for="(chapter, index) in renderedChapters"
          :key="chapter.id"
          class="chapter-tile"
          title="双击预览章节"
          :class="{
            selected: selected.has(chapter.id),
            'is-downloaded': archiveMode && downloadedChapters.has(chapter.id),
            'is-missing': archiveMode && !downloadedChapters.has(chapter.id),
          }"
          type="button"
          @click="handleChapterClick($event, chapter, index)"
          @contextmenu.prevent="enterChapterSelection($event, chapter, index)"
          @dblclick.prevent.stop="openChapterPreview(chapter)"
        >
          <component :is="selected.has(chapter.id) ? SquareCheck : Square" :size="16" />
          <span class="chapter-tile-title">{{ chapter.title }}</span>
          <span v-if="chapter.order" class="badge">#{{ chapter.order }}</span>
        </button>
      </div>
    </section>
  </div>

  <div v-if="previewOpen" class="modal-backdrop reader-backdrop" @click.self="closeChapterPreview">
    <div class="modal reader-modal remote-preview-modal">
      <div class="modal-head">
        <div>
          <h2>{{ previewTitle }}</h2>
          <p v-if="previewKind === 'images' && previewPageCount" class="muted small">
            共 {{ previewPageCount }} 页 · 翻页预览
          </p>
          <p v-else-if="previewKind === 'text' && previewPageCount" class="muted small">
            共 {{ previewPageCount }} 页 · 翻页阅读
          </p>
          <p v-else-if="previewKind === 'video' && previewPageCount" class="muted small">
            共 {{ previewPageCount }} 个媒体文件 · 浏览器播放器
          </p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭预览" @click="closeChapterPreview">
          <X :size="18" />
        </button>
      </div>
      <div v-if="previewLoading" class="empty">
        <Loader2 :size="24" class="spin" />
        <span>正在加载章节预览</span>
      </div>
      <div v-else-if="previewError" class="card empty">
        <p>{{ previewError }}</p>
      </div>
      <div v-else-if="previewKind === 'video' && previewVideoUrl" class="preview-video-shell">
        <video
          :key="previewVideoUrl"
          class="browser-video-player"
          :src="previewVideoUrl"
          controls
          playsinline
          preload="metadata"
        />
        <a class="btn secondary small" :href="previewVideoUrl" target="_blank" rel="noreferrer">
          新窗口播放
        </a>
      </div>
      <div v-else-if="previewKind === 'images' && previewCurrentImage" class="reader-page-stage">
        <img
          :key="previewCurrentImage"
          :src="previewCurrentImage"
          :alt="`第 ${previewPageIndex + 1} 页`"
          loading="eager"
          decoding="async"
        />
      </div>
      <article v-else-if="previewKind === 'text' && previewCurrentText" class="reader-text reader-page-text">
        {{ previewCurrentText }}
      </article>
      <div v-else class="empty">
        <span>（空章节）</span>
      </div>
      <div v-if="previewPageCount > 1" class="reader-pager">
        <button
          class="btn secondary small"
          type="button"
          :disabled="previewPageIndex <= 0"
          aria-label="上一页"
          @click="stepPreviewPage(-1)"
        >
          <ChevronLeft :size="17" />
          上一页
        </button>
        <span>{{ previewPageIndex + 1 }} / {{ previewPageCount }}</span>
        <button
          class="btn secondary small"
          type="button"
          :disabled="previewPageIndex >= previewPageCount - 1"
          aria-label="下一页"
          @click="stepPreviewPage(1)"
        >
          下一页
          <ChevronRight :size="17" />
        </button>
      </div>
    </div>
  </div>

  <div v-if="subscribeOpen" class="modal-backdrop detail-modal-backdrop" @click.self="subscribeOpen = false">
    <div class="modal modal-lg subscription-modal">
      <div class="modal-head">
        <div>
          <h2>订阅任务设置</h2>
          <p class="muted small">{{ comic?.title }}</p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="subscribeOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <label class="field">
          <span>Cron 表达式</span>
          <input
            v-model="subForm.cronExpr"
            class="input"
            placeholder="0 21 * * 5"
            :disabled="!autoDownload"
          />
          <span class="muted small">
            默认按当前整点生成每周检查周期，可改为任意 Cron 表达式；关掉「自动追更」会清空此项，留空即不启用定时检查。
          </span>
        </label>
        <label class="field">
          <span>下载位置</span>
          <DirectoryPicker
            v-model="subForm.downloadDir"
            v-model:suffix-value="subForm.seriesDir"
            :suffix="comic?.title"
            :convert-to-simplified="subForm.convertToSimplified"
            :allow-default="false"
            placeholder="选择下载位置"
            @change="rememberSubscriptionDownloadDir"
          />
        </label>
        <div class="function-option-grid">
          <FunctionOption label="章节标符" :active="subForm.chapterMarker" @toggle="subForm.chapterMarker = !subForm.chapterMarker" />
          <FunctionOption label="封面模糊" :active="subForm.coverBlur" @toggle="subForm.coverBlur = !subForm.coverBlur" />
          <FunctionOption label="繁转简" :active="subForm.convertToSimplified" @toggle="subForm.convertToSimplified = !subForm.convertToSimplified" />
          <FunctionOption label="历史章节" :active="subForm.allChapters" @toggle="subForm.allChapters = !subForm.allChapters" />
          <FunctionOption label="自动追更" :active="autoDownload" @toggle="toggleAutoUpdate" />
        </div>
      </div>
      <div class="modal-foot">
        <button class="btn secondary" type="button" @click="subscribeOpen = false">取消</button>
        <button class="btn" type="button" :disabled="busy" @click="confirmSubscribe">
          <Loader2 v-if="busy" :size="15" class="spin" />
          <Rss v-else :size="15" />
          创建订阅
        </button>
      </div>
    </div>
  </div>

  <div v-if="downloadOpen" class="modal-backdrop detail-modal-backdrop" @click.self="downloadOpen = false">
    <div class="modal modal-lg subscription-modal download-task-modal">
      <div class="modal-head">
        <div>
          <h2>下载任务设置</h2>
          <p class="muted small">{{ comic?.title }} · 已选 {{ selected.size }} 章</p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="downloadOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <label class="field">
          <span>下载位置</span>
          <DirectoryPicker
            v-model="downloadForm.downloadDir"
            v-model:suffix-value="downloadForm.seriesDir"
            :suffix="comic?.title"
            :convert-to-simplified="downloadForm.convertToSimplified"
            :allow-default="false"
            placeholder="选择下载位置"
            @change="rememberSubscriptionDownloadDir"
          />
        </label>
        <div class="function-option-grid">
          <FunctionOption label="章节标符" :active="downloadForm.chapterMarker" @toggle="downloadForm.chapterMarker = !downloadForm.chapterMarker" />
          <FunctionOption label="繁转简" :active="downloadForm.convertToSimplified" @toggle="downloadForm.convertToSimplified = !downloadForm.convertToSimplified" />
          <FunctionOption label="合并下载" :active="downloadForm.mergeChapters" @toggle="downloadForm.mergeChapters = !downloadForm.mergeChapters" />
        </div>
      </div>
      <div class="modal-foot">
        <button class="btn secondary" type="button" @click="downloadOpen = false">取消</button>
        <button class="btn download-btn" type="button" :disabled="busy" @click="downloadSelected">
          <Loader2 v-if="busy" :size="15" class="spin" />
          <Download v-else :size="15" />
          下载 ({{ selected.size }})
        </button>
      </div>
    </div>
  </div>
</template>
