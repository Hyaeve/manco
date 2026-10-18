<script setup>
import { computed, nextTick, onActivated, onDeactivated, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  BookOpen,
  CircleCheck,
  Compass,
  Loader2,
  RefreshCw,
  RotateCcw,
  Search,
  Trash2,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import CollectionEmptyState from '../components/CollectionEmptyState.vue'
import ContentLink from '../components/ContentLink.vue'
import { askConfirm } from '../stores/confirm'

const items = ref([])
const loading = ref(true)
const error = ref('')
const busy = ref(0)
const selected = ref(new Set())
const selecting = ref(false)
const route = useRoute()
const router = useRouter()
let lastIndex = -1
let timer = 0

const TASK_ROW_HEIGHT = 76
const TASK_ROW_GAP = 8
const TASK_ROW_STEP = TASK_ROW_HEIGHT + TASK_ROW_GAP
const TASK_VIRTUAL_OVERSCAN = 4
// 每次从服务端取多少条；滚动到已加载末尾时继续追加。
const TASK_PAGE_SIZE = 200
const virtualViewport = ref(null)
const virtualScrollTop = ref(0)
const virtualViewportHeight = ref(720)
const totalTasks = ref(0)
const loadingMore = ref(false)
let scrollContainer = null

const filters = [
  { value: 'all', label: '全部' },
  { value: 'active', label: '进行中' },
  { value: 'queued', label: '排队' },
  { value: 'failed', label: '失败' },
]

const filterValues = new Set(filters.map((item) => item.value))
const initialFilter = typeof route.query.filter === 'string' && filterValues.has(route.query.filter) ? route.query.filter : 'all'
const filter = ref(initialFilter)

function queryText(value) {
  return typeof value === 'string' ? value : ''
}

const keyword = ref(queryText(route.query.q).trim())
const searchInput = ref(keyword.value)

// 关键词过滤：作品名、章节名、作者任一命中即可，便于从订阅卡片直达失败项。
const scopedItems = computed(() => {
  const key = keyword.value.trim().toLowerCase()
  if (!key) return items.value
  return items.value.filter((item) => {
    const haystack = `${item.comicTitle || ''} ${item.chapterTitle || ''} ${item.comicAuthor || ''}`.toLowerCase()
    return haystack.includes(key)
  })
})

const nonCompletedItems = computed(() => scopedItems.value.filter((item) => item.status !== 'completed'))

const counts = computed(() => {
  const result = { all: nonCompletedItems.value.length, active: 0, queued: 0, failed: 0 }
  for (const item of nonCompletedItems.value) {
    if (item.status === 'running') result.active += 1
    if (['queued', 'waiting', 'paused'].includes(item.status)) result.queued += 1
    if (item.status === 'failed' || item.status === 'canceled') result.failed += 1
  }
  return result
})

const visible = computed(() => {
  if (filter.value === 'all') return nonCompletedItems.value
  if (filter.value === 'active') return nonCompletedItems.value.filter((item) => item.status === 'running')
  if (filter.value === 'queued') return nonCompletedItems.value.filter((item) => ['queued', 'waiting', 'paused'].includes(item.status))
  return nonCompletedItems.value.filter((item) => ['failed', 'canceled'].includes(item.status))
})

const virtualRange = computed(() => {
  const total = visible.value.length
  if (!total) return { start: 0, end: 0 }
  const start = Math.max(0, Math.floor(virtualScrollTop.value / TASK_ROW_STEP) - TASK_VIRTUAL_OVERSCAN)
  const visibleCount = Math.ceil(virtualViewportHeight.value / TASK_ROW_STEP)
  const end = Math.min(total, start + visibleCount + TASK_VIRTUAL_OVERSCAN * 2)
  return { start, end }
})

const virtualRows = computed(() =>
  visible.value.slice(virtualRange.value.start, virtualRange.value.end).map((item, offset) => ({
    item,
    index: virtualRange.value.start + offset,
  })),
)

const virtualInnerHeight = computed(() =>
  visible.value.length ? visible.value.length * TASK_ROW_STEP - TASK_ROW_GAP : 0,
)

const hasActive = computed(() => items.value.some((item) => ['queued', 'waiting', 'running', 'paused'].includes(item.status)))

watch(
  () => route.query.filter,
  (value) => {
    if (value === 'completed') {
      router.replace({ name: 'completed', query: route.query.q ? { q: route.query.q } : {} })
      return
    }
    const next = typeof value === 'string' && filterValues.has(value) ? value : 'all'
    if (filter.value !== next) filter.value = next
  },
)

watch(
  () => route.query.q,
  (value) => {
    const next = queryText(value).trim()
    if (keyword.value !== next) keyword.value = next
    if (searchInput.value !== next) searchInput.value = next
  },
)

function retryable(status) {
  return ['failed', 'canceled', 'paused'].includes(status)
}

const retryableVisible = computed(() => visible.value.filter((item) => retryable(item.status)))
const selectedRetryable = computed(() => retryableVisible.value.filter((item) => selected.value.has(item.id)))
const allSelected = computed(() => retryableVisible.value.length > 0 && selectedRetryable.value.length === retryableVisible.value.length)

function rowSelected(id) {
  return selected.value.has(id)
}

function enterSelect(item) {
  if (!selecting.value) selecting.value = true
  if (!selected.value.has(item.id)) {
    const next = new Set(selected.value)
    next.add(item.id)
    selected.value = next
  }
}

function onContextMenu(item, index, event) {
  event.preventDefault()
  if (event.shiftKey && lastIndex >= 0) {
    const next = new Set(selected.value)
    const from = Math.min(lastIndex, index)
    const to = Math.max(lastIndex, index)
    for (let cursor = from; cursor <= to; cursor += 1) {
      const row = visible.value[cursor]
      if (row) next.add(row.id)
    }
    selected.value = next
    selecting.value = true
    return
  }
  enterSelect(item)
  lastIndex = index
}

function toggleRow(item, index, event) {
  if (event?.shiftKey) event.preventDefault()
  const next = new Set(selected.value)
  if (event?.shiftKey && lastIndex >= 0) {
    const from = Math.min(lastIndex, index)
    const to = Math.max(lastIndex, index)
    for (let cursor = from; cursor <= to; cursor += 1) {
      const row = visible.value[cursor]
      if (row) next.add(row.id)
    }
  } else if (next.has(item.id)) {
    next.delete(item.id)
  } else {
    next.add(item.id)
  }
  selected.value = next
  selecting.value = next.size > 0
  lastIndex = index
}

function toggleSelectAll() {
  const next = new Set(selected.value)
  if (allSelected.value) {
    for (const item of retryableVisible.value) next.delete(item.id)
  } else {
    for (const item of retryableVisible.value) next.add(item.id)
  }
  selected.value = next
  selecting.value = next.size > 0
}

function clearSelection() {
  selected.value = new Set()
  selecting.value = false
  lastIndex = -1
}

// 虚拟列表跟随页面滚动：用页面滚动位置换算相对列表顶部的偏移。
function measureVirtual() {
  const viewport = virtualViewport.value
  if (!viewport) return
  const scrollTop = scrollContainer ? scrollContainer.scrollTop : 0
  const top = viewport.getBoundingClientRect().top + scrollTop - (scrollContainer ? scrollContainer.getBoundingClientRect().top : 0)
  virtualScrollTop.value = Math.max(0, scrollTop - top)
  const height = scrollContainer ? scrollContainer.clientHeight : window.innerHeight
  virtualViewportHeight.value = Math.max(320, height || 720)
  // 默认视图（无筛选）滚到已加载末尾时静默补下一页。
  if (filter.value === 'all' && !keyword.value.trim() && items.value.length < totalTasks.value) {
    if (virtualRange.value.end >= visible.value.length - TASK_VIRTUAL_OVERSCAN * 2) loadMoreTasks()
  }
}

function onPageScroll() {
  measureVirtual()
}

function onPageResize() {
  measureVirtual()
}

function resetVirtualScroll() {
  if (scrollContainer && virtualViewport.value) {
    const top = virtualViewport.value.getBoundingClientRect().top + scrollContainer.scrollTop - scrollContainer.getBoundingClientRect().top
    scrollContainer.scrollTop = Math.max(0, top - 12)
  } else if (scrollContainer) {
    scrollContainer.scrollTop = 0
  }
  virtualScrollTop.value = 0
}

watch([filter, keyword], resetVirtualScroll)

function onKeydown(event) {
  if (event.key === 'Escape' && selecting.value) clearSelection()
}

function stopPolling() {
  if (timer) {
    window.clearInterval(timer)
    timer = 0
  }
}

function startPolling() {
  stopPolling()
  timer = window.setInterval(() => {
    if (hasActive.value) load(true)
  }, 4000)
}

onMounted(async () => {
  await load()
  scrollContainer = virtualViewport.value?.closest('.content-scroll') || null
  scrollContainer?.addEventListener('scroll', onPageScroll, { passive: true })
  window.addEventListener('resize', onPageResize)
  await nextTick()
  measureVirtual()
  window.addEventListener('keydown', onKeydown)
  startPolling()
})

onUnmounted(() => {
  stopPolling()
  scrollContainer?.removeEventListener('scroll', onPageScroll)
  window.removeEventListener('resize', onPageResize)
  window.removeEventListener('keydown', onKeydown)
})

// 切走页面时退出多选，避免回到列表还停留在选中态。
onDeactivated(() => {
  clearSelection()
  stopPolling()
})

onActivated(() => {
  measureVirtual()
  startPolling()
})

async function load(silent = false) {
  if (!silent) loading.value = true
  try {
    // 只取第一页：任务历史可能上万条，整表拉下来既慢又占内存。
    const payload = await api.downloads(TASK_PAGE_SIZE, 0, 'active')
    const pageItems = payload.items || []
    if (silent && items.value.length) {
      const updates = new Map(pageItems.map((item) => [item.id, item]))
      const existing = new Set(items.value.map((item) => item.id))
      const merged = items.value.map((item) => updates.get(item.id) || item)
      for (const item of pageItems) {
        if (!existing.has(item.id)) merged.push(item)
      }
      items.value = merged
    } else {
      items.value = pageItems
    }
    totalTasks.value = Number(payload.total || items.value.length)
    if (silent) error.value = ''
  } catch (err) {
    if (!silent) error.value = err.message
  } finally {
    loading.value = false
  }
}

// 虚拟列表滚到已加载数据末尾时再取下一页。
async function loadMoreTasks() {
  if (loadingMore.value || items.value.length >= totalTasks.value) return
  loadingMore.value = true
  try {
    const payload = await api.downloads(TASK_PAGE_SIZE, items.value.length, 'active')
    const next = payload.items || []
    if (next.length) items.value = [...items.value, ...next]
    totalTasks.value = Number(payload.total ?? totalTasks.value)
    error.value = ''
  } catch (err) {
    error.value = err.message
  } finally {
    loadingMore.value = false
  }
}

function setFilter(value) {
  filter.value = value
  const query = { ...route.query, filter: value }
  router.replace({ name: 'downloads', query })
  resetVirtualScroll()
}

function applyKeyword() {
  keyword.value = searchInput.value.trim()
  const query = { ...route.query }
  if (keyword.value) query.q = keyword.value
  else delete query.q
  router.replace({ name: 'downloads', query })
  resetVirtualScroll()
}

function clearKeyword() {
  searchInput.value = ''
  applyKeyword()
}

// 任务列表状态只保留成功/失败/等待/下载中四类，详细错误请查看运行日志。
function statusLabel(status) {
  return (
    {
      queued: '等待',
      waiting: '等待',
      paused: '等待',
      running: '下载中',
      completed: '成功',
      failed: '失败',
      canceled: '失败',
    }[status] || status
  )
}

function statusClass(status) {
  if (status === 'completed') return 'success'
  if (status === 'running') return 'primary'
  if (status === 'failed' || status === 'canceled') return 'danger'
  if (status === 'waiting' || status === 'queued' || status === 'paused') return 'warning'
  return ''
}

function cover(item) {
  return api.imageUrl(item.comicCover, item.sourceId)
}

function formatTime(item) {
  const raw = item.finishedAt || item.updatedAt
  if (!raw) return '—'
  return new Date(raw).toLocaleString()
}

// 下载进度：完成态固定满格，进行中按已下载页数实时计算。
function progressPercent(item) {
  if (item.status === 'completed') return 100
  const total = Number(item.totalPages || 0)
  const completed = Number(item.completedPages || 0)
  if (total <= 0) return item.status === 'running' ? 6 : 0
  return Math.max(0, Math.min(100, Math.round((completed / total) * 100)))
}

async function retry(item) {
  busy.value = item.id
  error.value = ''
  try {
    await api.retryDownload(item.id)
    await load(true)
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = 0
  }
}

async function retrySelected() {
  const ids = selectedRetryable.value.map((item) => item.id)
  if (!ids.length) return
  busy.value = -1
  error.value = ''
  try {
    await api.retryDownloads(ids)
    clearSelection()
    await load(true)
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = 0
  }
}

// 批量删除：连同已下载的文件一起删除。
async function removeSelected() {
  const ids = [...selected.value]
  if (!ids.length || busy.value) return
  const confirmed = await askConfirm({
    title: '删除任务和文件',
    message: `确定删除所选 ${ids.length} 个任务记录，并删除它们已下载的文件吗？删除后无法恢复。`,
    confirmText: '删除',
    danger: true,
  })
  if (!confirmed) return
  busy.value = -2
  error.value = ''
  let failed = 0
  try {
    for (const id of ids) {
      try {
        await api.deleteDownload(id, true)
      } catch {
        failed += 1
      }
    }
    if (failed) error.value = `有 ${failed} 个任务删除失败，请查看运行日志`
    if (failed < ids.length) clearSelection()
    await load()
  } finally {
    busy.value = 0
  }
}

async function remove(item, removeFile) {
  const hint = removeFile
    ? `确定删除「${item.comicTitle} ${item.chapterTitle}」的任务记录并删除已下载文件吗？`
    : `确定删除「${item.comicTitle} ${item.chapterTitle}」的任务记录吗？下载文件会保留。`
  const confirmed = await askConfirm({
    title: removeFile ? '删除任务和文件' : '删除任务记录',
    message: hint,
    confirmText: '删除',
    danger: true,
  })
  if (!confirmed) return
  busy.value = item.id
  error.value = ''
  try {
    await api.deleteDownload(item.id, removeFile)
    await load()
    const next = new Set(selected.value)
    next.delete(item.id)
    selected.value = next
    if (!next.size) selecting.value = false
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = 0
  }
}
</script>

<template>
  <div v-if="error" class="alert error">{{ error }}</div>

  <div class="toolbar">
    <div class="segmented">
      <button
        v-for="item in filters"
        :key="item.value"
        type="button"
        :class="{ active: filter === item.value }"
        @click="setFilter(item.value)"
      >
        {{ item.label }}
        <span class="segmented-count">{{ counts[item.value] }}</span>
      </button>
      <ContentLink class="completed-tab" to="/completed">
        完成列表
      </ContentLink>
    </div>
    <span class="spacer" />
    <button class="btn secondary small" type="button" :disabled="loading" @click="load()">
      <RefreshCw :size="15" :class="{ spin: loading }" />
      刷新
    </button>
    <label class="task-search">
      <Search :size="15" />
      <input
        v-model="searchInput"
        class="task-search-input"
        type="search"
        placeholder="搜索作品或章节"
        @keyup.enter="applyKeyword"
        @search="applyKeyword"
      />
      <button v-if="searchInput" class="icon-btn" type="button" aria-label="清除搜索" @click="clearKeyword">
        <X :size="14" />
      </button>
    </label>
  </div>

  <div v-if="loading && !items.length" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载任务列表</span>
  </div>
  <CollectionEmptyState
    v-else-if="!items.length"
    class="page-empty-state"
    variant="tasks"
    window-title="任务列表"
    heading="还没有下载任务"
    description="下载任务会显示在这里。"
  >
    <template #actions>
      <ContentLink class="btn small" to="/discover">
        <Compass :size="15" />
        浏览内容源
      </ContentLink>
    </template>
  </CollectionEmptyState>
  <div v-else>
    <div v-if="!visible.length" class="card empty">
      <CircleCheck :size="26" />
      <p>当前筛选条件下没有任务。</p>
    </div>
    <div v-else class="download-table">
      <div class="download-head">
        <span>作品</span>
        <span>章节</span>
        <span>状态</span>
        <span>时间</span>
        <span>操作</span>
      </div>
      <div
        ref="virtualViewport"
        class="download-virtual-viewport"
      >
        <div class="download-virtual-inner" :style="{ height: `${virtualInnerHeight}px` }">
          <article
            v-for="entry in virtualRows"
            :key="entry.item.id"
            class="download-row"
            :class="{ selected: rowSelected(entry.item.id) }"
            :style="{ transform: `translateY(${entry.index * TASK_ROW_STEP}px)` }"
            @contextmenu="onContextMenu(entry.item, entry.index, $event)"
            @click="selecting ? toggleRow(entry.item, entry.index, $event) : null"
          >
            <ContentLink
              class="download-work"
              :to="{ name: 'comic', params: { sourceId: entry.item.sourceId, comicId: entry.item.comicId } }"
              :before-navigate="() => !selecting"
              :title="entry.item.comicTitle"
              @click.stop
            >
              {{ entry.item.comicTitle }}
            </ContentLink>
            <span class="download-chapter" :title="entry.item.chapterTitle">{{ entry.item.chapterTitle }}</span>
            <div class="download-status">
              <div class="download-status-head">
                <span class="badge" :class="statusClass(entry.item.status)">{{ statusLabel(entry.item.status) }}</span>
              </div>
              <div
                class="download-progress"
                :class="`is-${entry.item.status}`"
                role="progressbar"
                :aria-valuenow="progressPercent(entry.item)"
                aria-valuemin="0"
                aria-valuemax="100"
              >
                <span class="download-progress-fill" :style="{ width: `${progressPercent(entry.item)}%` }" />
              </div>
            </div>
            <div class="download-time">{{ formatTime(entry.item) }}</div>
            <div class="download-actions">
              <button
                v-if="retryable(entry.item.status)"
                class="icon-btn"
                type="button"
                :disabled="busy === entry.item.id"
                aria-label="重试"
                @click.stop="retry(entry.item)"
              >
                <RotateCcw :size="15" />
              </button>
              <button
                class="icon-btn danger"
                type="button"
                :disabled="busy === entry.item.id"
                aria-label="删除任务记录"
                @click.stop="remove(entry.item, false)"
              >
                <Trash2 :size="15" />
              </button>
            </div>
          </article>
        </div>
      </div>
    </div>

  </div>

  <div v-if="selecting" class="selection-pill">
    <span class="selection-count">已选 {{ selected.size }} 项</span>
    <button class="btn secondary small" type="button" @click="toggleSelectAll">
      {{ allSelected ? '取消全选' : '全选' }}
    </button>
    <button class="btn secondary small" type="button" @click="clearSelection">
      <X :size="14" />
      清除
    </button>
    <button
      class="btn primary small"
      type="button"
      :disabled="!selectedRetryable.length || busy === -1"
      @click="retrySelected"
    >
      <Loader2 v-if="busy === -1" :size="14" class="spin" />
      <RotateCcw v-else :size="14" />
      重试
    </button>
    <button
      class="btn danger small"
      type="button"
      :disabled="!selected.size || busy !== 0"
      @click="removeSelected"
    >
      <Loader2 v-if="busy === -2" :size="14" class="spin" />
      <Trash2 v-else :size="14" />
      删除
    </button>
  </div>
</template>
