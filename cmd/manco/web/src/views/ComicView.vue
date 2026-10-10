<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ArrowDownWideNarrow,
  ArrowUpWideNarrow,
  BookOpen,
  ChevronLeft,
  Download,
  Languages,
  Loader2,
  Rss,
  Search,
  Square,
  SquareCheck,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import DirectoryPicker from '../components/DirectoryPicker.vue'
import { notify } from '../stores/notices'

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
let anchorIndex = -1

const subscribeOpen = ref(false)
const subForm = ref({
  cronExpr: '',
  downloadDir: '',
  convertToSimplified: true,
  allChapters: false,
})

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
const allSelected = computed(
  () => displayChapters.value.length > 0 && displayChapters.value.every((chapter) => selected.value.has(chapter.id)),
)
const heroStyle = computed(() => ({ '--hero-tint': heroTint.value || 'transparent' }))

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
    const payload = await api.sources()
    const source = (payload.items || []).find((item) => item.id === sourceId)
    isBook.value = source?.kind === 'book'
  } catch {
    isBook.value = false
  }
  if (comic.value?.cover) extractTint(cover())
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
  error.value = ''
  subForm.value = {
    cronExpr: subForm.value.cronExpr || defaultCron(),
    downloadDir: subForm.value.downloadDir,
    convertToSimplified: true,
    allChapters: false,
  }
  subscribeOpen.value = true
}

async function confirmSubscribe() {
  if (!comic.value) return
  if (!subForm.value.cronExpr.trim()) {
    notify('Cron 表达式不能为空', 'warning')
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
    })
    subscribeOpen.value = false
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

async function downloadSelected() {
  const picked = chapters.value.filter((item) => selected.value.has(item.id))
  if (!picked.length || !comic.value) return
  busy.value = true
  error.value = ''
  try {
    const result = await api.createDownload({
      sourceId,
      comicId: comic.value.id,
      comicTitle: comic.value.title,
      comicCover: comic.value.cover,
      autoDownload: autoDownload.value,
      chapters: picked,
      downloadDir: subForm.value.downloadDir.trim(),
      convertToSimplified: Boolean(subForm.value.convertToSimplified),
    })
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
      <div class="detail-cover">
        <button class="detail-cover-back" type="button" aria-label="返回" title="返回" @click="goBack">
          <ChevronLeft :size="20" />
        </button>
        <img v-if="comic.cover" :src="cover()" :alt="comic.title" />
        <span v-else class="cover-fallback"><BookOpen :size="28" /></span>
      </div>
      <div class="detail-info">
        <div class="detail-info-head">
          <h2>{{ comic.title }}</h2>
          <div class="detail-actions">
            <button class="btn secondary" type="button" :disabled="busy || !selected.size" @click="downloadSelected">
              <Loader2 v-if="busy" :size="16" class="spin" />
              <Download v-else :size="16" />
              下载所选（{{ selected.size }}）
            </button>
            <button class="btn" type="button" :disabled="busy" @click="openSubscribe">
              <Rss :size="16" />
              订阅
            </button>
          </div>
        </div>
        <div class="meta">
          <span>{{ comic.author || '未知作者' }}</span>
          <span v-if="comic.status"> · {{ comic.status }}</span>
        </div>
        <div v-if="comic.tags?.length" class="tag-list">
          <span v-for="tag in comic.tags" :key="tag" class="badge">{{ tag }}</span>
        </div>
        <p class="description">{{ comic.description || '暂无简介' }}</p>
      </div>
    </div>

    <section class="detail-chapters">
      <div class="detail-chapter-head">
        <button class="btn ghost small" type="button" @click="toggleAll">
          <component :is="allSelected ? SquareCheck : Square" :size="16" />
          全选
        </button>
        <button class="btn ghost small" type="button" @click="sortDesc = !sortDesc">
          <component :is="sortDesc ? ArrowDownWideNarrow : ArrowUpWideNarrow" :size="16" />
          {{ sortDesc ? '最新在前' : '最旧在前' }}
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
          v-for="(chapter, index) in displayChapters"
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
    <div class="modal">
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
          <DirectoryPicker v-model="subForm.downloadDir" :suffix="comic?.title" />
        </label>
        <label class="switch-row">
          <input v-model="subForm.convertToSimplified" type="checkbox" />
          <span><Languages :size="14" /> 下载时执行繁体转简体</span>
        </label>
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
</template>
