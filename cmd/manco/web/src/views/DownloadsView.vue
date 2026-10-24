<script setup>
import { computed, onActivated, onDeactivated, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
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
import PaginationBar from '../components/PaginationBar.vue'
import { askConfirm } from '../stores/confirm'

const items = shallowRef([])
const selectedSnapshots = ref(new Map())
const loading = ref(true)
const error = ref('')
const busy = ref(0)
const selected = ref(new Set())
const selecting = ref(false)
const route = useRoute()
const router = useRouter()
let lastIndex = -1
let timer = 0

const TASK_PAGE_SIZE = 30
const taskPage = ref(1)
const taskPageLoading = ref(false)
const taskRefreshing = ref(false)
const totalTasks = ref(0)
let requestGeneration = 0
let loadController = null
let viewActive = true
const serverCounts = ref({ all: 0, active: 0, queued: 0, failed: 0 })

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

const counts = computed(() => serverCounts.value)
const visible = computed(() => items.value)
const taskOffset = computed(() => (taskPage.value - 1) * TASK_PAGE_SIZE)

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
const selectedRetryable = computed(() =>
  [...selectedSnapshots.value.values()].filter((item) => selected.value.has(item.id) && retryable(item.status)),
)
const allSelected = computed(() =>
  retryableVisible.value.length > 0 && retryableVisible.value.every((item) => selected.value.has(item.id)),
)

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
  selectedSnapshots.value.set(item.id, item)
}

function onContextMenu(item, index, event) {
  event.preventDefault()
  if (event.shiftKey && lastIndex >= 0) {
    const next = new Set(selected.value)
    const from = Math.min(lastIndex, index)
    const to = Math.max(lastIndex, index)
    for (let cursor = from; cursor <= to; cursor += 1) {
      const row = visible.value[cursor - taskOffset.value]
      if (row) {
        next.add(row.id)
        selectedSnapshots.value.set(row.id, row)
      }
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
      const row = visible.value[cursor - taskOffset.value]
      if (row) {
        next.add(row.id)
        selectedSnapshots.value.set(row.id, row)
      }
    }
  } else if (next.has(item.id)) {
    next.delete(item.id)
    selectedSnapshots.value.delete(item.id)
  } else {
    next.add(item.id)
    selectedSnapshots.value.set(item.id, item)
  }
  selected.value = next
  selecting.value = next.size > 0
  lastIndex = index
}

function toggleSelectAll() {
  const next = new Set(selected.value)
  if (allSelected.value) {
    for (const item of retryableVisible.value) {
      next.delete(item.id)
      selectedSnapshots.value.delete(item.id)
    }
  } else {
    for (const item of retryableVisible.value) {
      next.add(item.id)
      selectedSnapshots.value.set(item.id, item)
    }
  }
  selected.value = next
  selecting.value = next.size > 0
}

function clearSelection() {
  selected.value = new Set()
  selectedSnapshots.value = new Map()
  selecting.value = false
  lastIndex = -1
}

function scrollTasksToTop() {
  const scroller = document.querySelector('.content-scroll')
  if (scroller) scroller.scrollTop = 0
}

watch([filter, keyword], () => {
  clearSelection()
  scrollTasksToTop()
  load(false, true)
})

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
    if (viewActive && !document.hidden && !loading.value && !taskPageLoading.value && !taskRefreshing.value) load(true)
  }, 4000)
}

onMounted(async () => {
  await load()
  window.addEventListener('keydown', onKeydown)
  startPolling()
})

onUnmounted(() => {
  viewActive = false
  loadController?.abort()
  stopPolling()
  window.removeEventListener('keydown', onKeydown)
})

// 切走页面时退出多选，避免回到列表还停留在选中态。
onDeactivated(() => {
  viewActive = false
  loadController?.abort()
  clearSelection()
  stopPolling()
})

onActivated(() => {
  viewActive = true
  if (!items.value.length && !loading.value) load()
  startPolling()
})

async function loadTaskPage(page = taskPage.value, silent = false) {
  const generation = ++requestGeneration
  loadController?.abort()
  loadController = new AbortController()
  const signal = loadController.signal
  const requestedPage = Math.max(1, Number(page) || 1)
  const offset = (requestedPage - 1) * TASK_PAGE_SIZE
  if (silent) taskRefreshing.value = true
  else if (items.value.length) taskPageLoading.value = true
  else loading.value = true
  try {
    const options = { filter: filter.value, query: keyword.value, compact: true, signal }
    const payload = await api.downloads(TASK_PAGE_SIZE, offset, 'active', options)
    if (signal.aborted || generation !== requestGeneration) return
    const pageItems = payload.items || []
    const nextTotal = Number(payload.total || 0)
    const maxPage = Math.max(1, Math.ceil(nextTotal / TASK_PAGE_SIZE))
    if (!pageItems.length && requestedPage > maxPage) {
      await loadTaskPage(maxPage, silent)
      return
    }
    items.value = pageItems
    taskPage.value = requestedPage
    totalTasks.value = nextTotal
    serverCounts.value = payload.counts || { all: totalTasks.value, active: 0, queued: 0, failed: 0 }
    for (const item of pageItems) {
      if (selected.value.has(item.id)) selectedSnapshots.value.set(item.id, item)
    }
    if (silent) error.value = ''
  } catch (err) {
    if (!signal.aborted && generation === requestGeneration && !silent) error.value = err.message
  } finally {
    if (generation === requestGeneration) {
      loading.value = false
      taskPageLoading.value = false
      taskRefreshing.value = false
    }
  }
}

async function load(silent = false, resetPage = false) {
  if (resetPage) {
    taskPage.value = 1
  }
  await loadTaskPage(resetPage ? 1 : taskPage.value, silent)
}

function goToTaskPage(page) {
  if (taskPageLoading.value || page === taskPage.value) return
  lastIndex = -1
  scrollTasksToTop()
  loadTaskPage(page)
}

function setFilter(value) {
  filter.value = value
  const query = { ...route.query, filter: value }
  router.replace({ name: 'downloads', query })
}

function applyKeyword() {
  keyword.value = searchInput.value.trim()
  const query = { ...route.query }
  if (keyword.value) query.q = keyword.value
  else delete query.q
  router.replace({ name: 'downloads', query })
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
    await load(true)
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
    await load(true)
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
    <button class="btn secondary small" type="button" :disabled="loading || taskPageLoading || taskRefreshing" @click="load()">
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
    v-else-if="!totalTasks && !keyword"
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
    <div v-if="!items.length" class="card empty">
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
      <div class="task-page-rows" :class="{ 'is-page-loading': taskPageLoading }" :aria-busy="taskPageLoading">
          <article
            v-for="(entry, rowIndex) in visible"
            :key="entry.id"
            class="download-row"
            :class="{ selected: rowSelected(entry.id) }"
            @contextmenu="onContextMenu(entry, taskOffset + rowIndex, $event)"
            @click="selecting ? toggleRow(entry, taskOffset + rowIndex, $event) : null"
          >
            <ContentLink
              v-if="!selecting"
              class="download-work"
              :to="{ name: 'comic', params: { sourceId: entry.sourceId, comicId: entry.comicId } }"
              :title="entry.comicTitle"
              @click.stop
            >
              {{ entry.comicTitle }}
            </ContentLink>
            <span v-else class="download-work is-selection-label" :title="entry.comicTitle">
              {{ entry.comicTitle }}
            </span>
            <span class="download-chapter" :title="entry.chapterTitle">{{ entry.chapterTitle }}</span>
            <div class="download-status">
              <div class="download-status-head">
                <span class="badge" :class="statusClass(entry.status)">{{ statusLabel(entry.status) }}</span>
              </div>
              <div
                class="download-progress"
                :class="`is-${entry.status}`"
                role="progressbar"
                :aria-valuenow="progressPercent(entry)"
                aria-valuemin="0"
                aria-valuemax="100"
              >
                <span class="download-progress-fill" :style="{ width: `${progressPercent(entry)}%` }" />
              </div>
            </div>
            <div class="download-time">{{ formatTime(entry) }}</div>
            <div class="download-actions">
              <button
                v-if="retryable(entry.status)"
                class="icon-btn"
                type="button"
                :disabled="busy === entry.id"
                aria-label="重试"
                @click.stop="retry(entry)"
              >
                <RotateCcw :size="15" />
              </button>
              <button
                class="icon-btn danger"
                type="button"
                :disabled="busy === entry.id"
                aria-label="删除任务记录"
                @click.stop="remove(entry, false)"
              >
                <Trash2 :size="15" />
              </button>
            </div>
          </article>
      </div>
    </div>
    <PaginationBar
      class="task-pagination"
      :page="taskPage"
      :page-size="TASK_PAGE_SIZE"
      :total="totalTasks"
      :disabled="loading || taskPageLoading"
      @change="goToTaskPage"
    />

  </div>

  <div v-if="selecting" class="selection-pill task-selection-pill">
    <span class="selection-count">已选 {{ selected.size }} 项</span>
    <button class="btn secondary small" type="button" @click="toggleSelectAll">
      {{ allSelected ? '取消本页' : '全选本页' }}
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

<style scoped>
.task-page-rows {
  display: flex;
  flex-direction: column;
  gap: 8px;
  transition: opacity 0.18s ease;
}

.task-page-rows.is-page-loading {
  pointer-events: none;
  opacity: 0.58;
}

.download-work.is-selection-label {
  display: block;
  cursor: default;
}

.download-work.is-selection-label:hover {
  color: var(--text);
}

.task-pagination {
  margin-top: 18px;
}
</style>
