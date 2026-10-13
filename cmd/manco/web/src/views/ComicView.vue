<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  BookOpen,
  ClockArrowDown,
  ClockArrowUp,
  ArrowLeft,
  CircleCheck,
  Download,
  Loader2,
  Rss,
  Search,
  Tag,
  UserRound,
  Square,
  SquareCheck,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import DirectoryPicker from '../components/DirectoryPicker.vue'
import { notify } from '../stores/notices'
import { lastSubscriptionDownloadDir, rememberSubscriptionDownloadDir } from '../stores/subscriptionPreferences'

const route = useRoute()
const router = useRouter()
const sourceId = route.params.sourceId
const comicId = route.params.comicId
const isBook = ref(false)

const detail = ref(null)
const selected = ref(new Set())
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const autoDownload = ref(true)
const sortDesc = ref(false)
const queryInput = ref('')
const appliedQuery = ref('')
const heroTint = ref('')
const chapterRenderLimit = ref(80)
let pageScroller = null
let anchorIndex = -1

const subscribeOpen = ref(false)
const downloadOpen = ref(false)
const subForm = ref({
  cronExpr: '',
  downloadDir: lastSubscriptionDownloadDir(),
  convertToSimplified: true,
  chapterMarker: false,
  coverBlur: false,
  allChapters: false,
})
const downloadForm = ref({
  downloadDir: lastSubscriptionDownloadDir(),
  convertToSimplified: true,
  chapterMarker: false,
})

const existingSubscription = ref(null)
const isSubscribed = computed(() => Boolean(existingSubscription.value))

const comic = computed(() => detail.value?.comic || null)
const chapters = computed(() => detail.value?.chapters || [])
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

function defaultCron() {
  const now = new Date()
  return `${now.getMinutes()} ${now.getHours()} * * ${now.getDay()}`
}

onMounted(async () => {
  try {
    detail.value = await api.comic(sourceId, comicId)
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
  try {
    const [sourcePayload, subscriptionPayload] = await Promise.all([
      api.sources().catch(() => ({ items: [] })),
      api.subscriptions().catch(() => ({ items: [] })),
    ])
    const source = (sourcePayload.items || []).find((item) => item.id === sourceId)
    isBook.value = source?.kind === 'book'
    existingSubscription.value =
      (subscriptionPayload.items || []).find((item) => {
        if (String(item.sourceId) !== String(sourceId)) return false
        return [comicId, comic.value?.id].some((id) => String(id) === String(item.comicId))
      }) || null
    if (!lastSubscriptionDownloadDir() && subscriptionPayload.items?.length) {
      const latestDownloadDir = subscriptionPayload.items[0].downloadDir || ''
      subForm.value.downloadDir = latestDownloadDir
      rememberSubscriptionDownloadDir(latestDownloadDir)
    }
  } catch {
    isBook.value = false
  }
  if (comic.value?.cover) extractTint(cover())
  pageScroller = document.querySelector('.content-scroll')
  pageScroller?.addEventListener('scroll', handlePageScroll, { passive: true })
})

onUnmounted(() => {
  pageScroller?.removeEventListener('scroll', handlePageScroll)
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

function searchAuthor() {
  const author = String(comic.value?.author || '').trim()
  if (!author) return
  router.push({ name: 'search', query: { q: author, kind: isBook.value ? 'book' : 'comic' } })
}

function handleChapterClick(event, chapter, index) {
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
}

function toggleAll() {
  if (allSelected.value) {
    const next = new Set(selected.value)
    for (const chapter of displayChapters.value) next.delete(chapter.id)
    selected.value = next
  } else {
    selected.value = new Set([...selected.value, ...displayChapters.value.map((item) => item.id)])
  }
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
    cronExpr: subForm.value.cronExpr || defaultCron(),
    downloadDir: subForm.value.downloadDir || lastSubscriptionDownloadDir(),
    convertToSimplified: true,
    chapterMarker: false,
    coverBlur: false,
    allChapters: false,
  }
  subscribeOpen.value = true
}

async function confirmSubscribe() {
  if (!comic.value) return
  if (isSubscribed.value) {
    notify('该作品已在订阅清单中', 'info')
    return
  }
  if (!subForm.value.cronExpr.trim()) {
    notify('Cron 表达式不能为空', 'warning')
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
      autoDownload: autoDownload.value,
      enabled: true,
      baseline: subForm.value.allChapters ? '' : last?.id || '',
      lastChapterOrder: subForm.value.allChapters ? 0 : last?.order || 0,
      allChapters: subForm.value.allChapters,
      cronExpr: subForm.value.cronExpr.trim(),
      downloadDir: subForm.value.downloadDir.trim(),
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
    downloadDir: subForm.value.downloadDir || lastSubscriptionDownloadDir(),
    convertToSimplified: true,
    chapterMarker: false,
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
      autoDownload: autoDownload.value,
      chapters: picked,
      downloadDir: downloadForm.value.downloadDir.trim(),
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
        <ArrowLeft :size="24" :stroke-width="1.7" />
      </button>
      <div class="detail-cover-wrap">
        <div class="detail-cover">
        <img v-if="comic.cover" :src="cover()" :alt="comic.title" />
        <span v-else class="cover-fallback"><BookOpen :size="28" /></span>
        </div>
      </div>
      <div class="detail-info">
        <div class="detail-info-head">
          <h2>{{ comic.title }}</h2>
          <div class="detail-actions">
            <button class="btn download-btn" type="button" :disabled="busy || !selected.size" @click="openDownloadModal">
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
          <button v-if="comic.author" class="author-pill" type="button" @click="searchAuthor">
            <UserRound :size="14" />
            {{ comic.author }}
          </button>
          <span v-else>未知作者</span>
          <span v-if="comic.status"> · {{ comic.status }}</span>
        </div>
        <div v-if="comic.tags?.length" class="tag-list">
          <span v-for="tag in comic.tags" :key="tag" class="badge tag-pill">
            <Tag :size="12" />{{ tag }}
          </span>
        </div>
        <p class="description">{{ comic.description || '暂无简介' }}</p>
      </div>
    </div>

    <section class="detail-chapters">
      <div class="detail-chapter-head">
        <button class="btn ghost small select-all-btn" type="button" @click="toggleAll">
          <component :is="allSelected ? SquareCheck : Square" :size="16" />
          <span>全选</span>
        </button>
        <button class="btn ghost small sort-btn" type="button" :aria-label="sortDesc ? '最新在前' : '最旧在前'" @click="sortDesc = !sortDesc">
          <component :is="sortDesc ? ClockArrowDown : ClockArrowUp" :size="17" :stroke-width="1.7" />
        </button>
        <span class="muted small">共 {{ chapters.length }} 章</span>
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
      <div v-else class="chapter-grid">
        <button
          v-for="(chapter, index) in renderedChapters"
          :key="chapter.id"
          class="chapter-tile"
          :class="{ selected: selected.has(chapter.id) }"
          type="button"
          @click="handleChapterClick($event, chapter, index)"
        >
          <component :is="selected.has(chapter.id) ? SquareCheck : Square" :size="16" />
          <span class="chapter-tile-title">{{ chapter.title }}</span>
          <span v-if="chapter.order" class="badge">#{{ chapter.order }}</span>
        </button>
      </div>
    </section>
  </div>

  <div v-if="subscribeOpen" class="modal-backdrop" @click.self="subscribeOpen = false">
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
          <input v-model="subForm.cronExpr" class="input" placeholder="0 21 * * 5" />
          <span class="muted small">默认按当前整点生成每周检查周期，可改为任意 Cron 表达式。</span>
        </label>
        <label class="field">
          <span>下载位置</span>
          <DirectoryPicker
            v-model="subForm.downloadDir"
            :suffix="comic?.title"
            :allow-default="false"
            placeholder="选择下载位置"
            @change="rememberSubscriptionDownloadDir"
          />
        </label>
        <div class="subscription-toggle-grid">
          <label class="switch-row">
            <input v-model="subForm.chapterMarker" type="checkbox" />
            <span>章节标符</span>
          </label>
          <label class="switch-row">
            <input v-model="subForm.coverBlur" type="checkbox" />
            <span>封面模糊</span>
          </label>
          <label class="switch-row">
            <input v-model="subForm.convertToSimplified" type="checkbox" />
            <span>繁转简</span>
          </label>
        </div>
        <label class="switch-row">
          <input v-model="subForm.allChapters" type="checkbox" />
          <span><BookOpen :size="14" /> 订阅全部章节（旧章节一并下载）</span>
        </label>
        <label class="switch-row">
          <input v-model="autoDownload" type="checkbox" />
          <span>开启自动追更</span>
        </label>
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

  <div v-if="downloadOpen" class="modal-backdrop" @click.self="downloadOpen = false">
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
            :suffix="comic?.title"
            :allow-default="false"
            placeholder="选择下载位置"
            @change="rememberSubscriptionDownloadDir"
          />
        </label>
        <div class="subscription-toggle-grid download-toggle-grid">
          <label class="switch-row">
            <input v-model="downloadForm.chapterMarker" type="checkbox" />
            <span>章节标符</span>
          </label>
          <label class="switch-row">
            <input v-model="downloadForm.convertToSimplified" type="checkbox" />
            <span>繁转简</span>
          </label>
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
