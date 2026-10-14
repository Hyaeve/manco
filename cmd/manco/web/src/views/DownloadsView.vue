<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import {
  BookOpen,
  ChevronDown,
  ChevronRight,
  CircleCheck,
  Compass,
  FileArchive,
  FolderOpen,
  Loader2,
  RefreshCw,
  RotateCcw,
  Trash2,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import { askConfirm } from '../stores/confirm'

const items = ref([])
const loading = ref(true)
const error = ref('')
const filter = ref('all')
const busy = ref(0)
const selected = ref(new Set())
const selecting = ref(false)
const expandedGroups = ref(new Set())
let lastIndex = -1
let timer = 0

const filters = [
  { value: 'all', label: '全部' },
  { value: 'active', label: '进行中' },
  { value: 'queued', label: '排队' },
  { value: 'completed', label: '已完成' },
  { value: 'failed', label: '失败' },
]

const counts = computed(() => {
  const result = { all: items.value.length, active: 0, queued: 0, completed: 0, failed: 0 }
  for (const item of items.value) {
    if (item.status === 'running') result.active += 1
    if (['queued', 'waiting', 'paused'].includes(item.status)) result.queued += 1
    if (item.status === 'completed') result.completed += 1
    if (item.status === 'failed' || item.status === 'canceled') result.failed += 1
  }
  return result
})

const visible = computed(() => {
  if (filter.value === 'all') return items.value
  if (filter.value === 'active') return items.value.filter((item) => item.status === 'running')
  if (filter.value === 'queued') return items.value.filter((item) => ['queued', 'waiting', 'paused'].includes(item.status))
  if (filter.value === 'completed') return items.value.filter((item) => item.status === 'completed')
  return items.value.filter((item) => ['failed', 'canceled'].includes(item.status))
})

const hasActive = computed(() => items.value.some((item) => ['queued', 'waiting', 'running', 'paused'].includes(item.status)))

const completedGroups = computed(() => {
  const groups = new Map()
  for (const item of visible.value) {
    const key = `${item.sourceId || ''}:${item.comicId || item.comicTitle || item.id}`
    let group = groups.get(key)
    if (!group) {
      group = {
        key,
        sourceId: item.sourceId,
        comicId: item.comicId,
        title: item.comicTitle || '未命名作品',
        cover: item.comicCover,
        author: item.comicAuthor || '未知作者',
        items: [],
      }
      groups.set(key, group)
    }
    group.items.push(item)
  }
  return [...groups.values()].map((group) => {
    group.items.sort((left, right) => {
      const leftTime = new Date(left.finishedAt || left.updatedAt || 0).getTime()
      const rightTime = new Date(right.finishedAt || right.updatedAt || 0).getTime()
      return rightTime - leftTime
    })
    return group
  })
})

function isGroupExpanded(key) {
  return expandedGroups.value.has(key)
}

function toggleGroup(key) {
  const next = new Set(expandedGroups.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expandedGroups.value = next
}

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

function onKeydown(event) {
  if (event.key === 'Escape' && selecting.value) clearSelection()
}

onMounted(async () => {
  await load()
  window.addEventListener('keydown', onKeydown)
  timer = window.setInterval(() => {
    if (hasActive.value) load(true)
  }, 4000)
})

onUnmounted(() => {
  if (timer) window.clearInterval(timer)
  window.removeEventListener('keydown', onKeydown)
})

async function load(silent = false) {
  if (!silent) loading.value = true
  try {
    const payload = await api.downloads(300)
    items.value = payload.items || []
    if (silent) error.value = ''
  } catch (err) {
    if (!silent) error.value = err.message
  } finally {
    loading.value = false
  }
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

function formatSize(size) {
  const bytes = Number(size || 0)
  if (!bytes) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${unit === 0 ? value : value.toFixed(value >= 10 ? 0 : 1)} ${units[unit]}`
}

// 下载进度：完成态固定满格，进行中按已下载页数实时计算。
function progressPercent(item) {
  if (item.status === 'completed') return 100
  const total = Number(item.totalPages || 0)
  const completed = Number(item.completedPages || 0)
  if (total <= 0) return item.status === 'running' ? 6 : 0
  return Math.max(0, Math.min(100, Math.round((completed / total) * 100)))
}

function progressText(item) {
  if (item.status === 'completed') return '100%'
  const total = Number(item.totalPages || 0)
  const completed = Number(item.completedPages || 0)
  if (total <= 0) return item.status === 'running' ? '准备中' : '—'
  return `${completed}/${total}`
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

async function remove(item, removeFile) {
  const hint = removeFile
    ? `确定删除「${item.comicTitle} ${item.chapterTitle}」的任务记录并删除已下载的 CBZ 文件吗？`
    : `确定删除「${item.comicTitle} ${item.chapterTitle}」的任务记录吗？CBZ 文件会保留。`
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
    items.value = items.value.filter((row) => row.id !== item.id)
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
        @click="filter = item.value"
      >
        {{ item.label }}
        <span class="segmented-count">{{ counts[item.value] }}</span>
      </button>
    </div>
    <span class="spacer" />
    <button class="btn secondary small" type="button" :disabled="loading" @click="load()">
      <RefreshCw :size="15" :class="{ spin: loading }" />
      刷新
    </button>
  </div>

  <div v-if="loading && !items.length" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载任务列表</span>
  </div>
  <div v-else-if="!items.length" class="card empty">
    <FileArchive :size="26" />
    <p>还没有下载任务。每个章节会单独打包成一个 CBZ 文件。</p>
    <RouterLink class="btn small" to="/discover">
      <Compass :size="15" />
      浏览漫画源
    </RouterLink>
  </div>
  <div v-else-if="!visible.length" class="card empty">
    <CircleCheck :size="26" />
    <p>当前筛选条件下没有任务。</p>
  </div>
  <div v-else-if="filter === 'completed'" class="completed-task-groups">
    <article v-for="group in completedGroups" :key="group.key" class="completed-task-group">
      <button
        class="completed-task-head"
        type="button"
        :aria-expanded="isGroupExpanded(group.key)"
        @click="toggleGroup(group.key)"
      >
        <span class="completed-task-title">
          <strong>{{ group.title }}</strong>
          <small>{{ group.author }}</small>
        </span>
        <span class="completed-task-summary">
          <span class="badge success">已完成 {{ group.items.length }} 章</span>
        </span>
        <ChevronDown v-if="isGroupExpanded(group.key)" :size="18" />
        <ChevronRight v-else :size="18" />
      </button>
      <div v-if="isGroupExpanded(group.key)" class="completed-task-chapters">
        <div v-for="item in group.items" :key="item.id" class="completed-task-chapter">
          <span class="completed-chapter-title" :title="item.chapterTitle">{{ item.chapterTitle }}</span>
          <span class="completed-chapter-time">{{ formatTime(item) }}</span>
          <button class="icon-btn danger" type="button" :disabled="busy === item.id" aria-label="删除任务记录" @click="remove(item, false)">
            <Trash2 :size="15" />
          </button>
        </div>
      </div>
    </article>
  </div>
  <div v-else class="download-table">
    <div class="download-head">
      <span>作品章节</span>
      <span>状态</span>
      <span>文件大小</span>
      <span>时间</span>
      <span>操作</span>
    </div>
    <article
      v-for="(item, index) in visible"
      :key="item.id"
      class="download-row"
      :class="{ selected: rowSelected(item.id) }"
      @contextmenu="onContextMenu(item, index, $event)"
      @click="selecting ? toggleRow(item, index, $event) : null"
    >
      <div class="download-main">
        <RouterLink
          class="comic-title"
          :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.comicId } }"
          @click.stop
        >
          {{ item.comicTitle }}
        </RouterLink>
        <span class="download-chapter" :title="item.chapterTitle">{{ item.chapterTitle }}</span>
      </div>
      <div class="download-status">
        <div class="download-status-head">
          <span class="badge" :class="statusClass(item.status)">{{ statusLabel(item.status) }}</span>
          <span class="download-progress-text">{{ progressText(item) }}</span>
        </div>
        <div class="download-progress" :class="`is-${item.status}`" role="progressbar" :aria-valuenow="progressPercent(item)" aria-valuemin="0" aria-valuemax="100">
          <span class="download-progress-fill" :style="{ width: `${progressPercent(item)}%` }" />
        </div>
      </div>
      <div class="download-size">{{ formatSize(item.size) }}</div>
      <div class="download-time">{{ formatTime(item) }}</div>
      <div class="download-actions">
        <button
          v-if="retryable(item.status)"
          class="icon-btn"
          type="button"
          :disabled="busy === item.id"
          aria-label="重试"
          @click.stop="retry(item)"
        >
          <RotateCcw :size="15" />
        </button>
        <button
          v-if="item.status === 'completed' && item.filePath"
          class="icon-btn"
          type="button"
          :disabled="busy === item.id"
          aria-label="删除任务和文件"
          @click.stop="remove(item, true)"
        >
          <FolderOpen :size="15" />
        </button>
        <button
          class="icon-btn danger"
          type="button"
          :disabled="busy === item.id"
          aria-label="删除任务记录"
          @click.stop="remove(item, false)"
        >
          <Trash2 :size="15" />
        </button>
      </div>
    </article>
  </div>

  <div v-if="selecting" class="selection-pill">
    <span class="selection-count">已选 {{ selected.size }} 项</span>
    <button class="btn secondary small" type="button" @click="toggleSelectAll">
      {{ allSelected ? '取消全选' : '全选' }}
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
    <button class="btn secondary small" type="button" @click="clearSelection">
      <X :size="14" />
      清除
    </button>
  </div>
</template>
