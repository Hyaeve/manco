<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import {
  BookOpen,
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
    if (item.status === 'queued' || item.status === 'paused') result.queued += 1
    if (item.status === 'completed') result.completed += 1
    if (item.status === 'failed' || item.status === 'canceled') result.failed += 1
  }
  return result
})

const visible = computed(() => {
  if (filter.value === 'all') return items.value
  if (filter.value === 'active') return items.value.filter((item) => item.status === 'running')
  if (filter.value === 'queued') return items.value.filter((item) => ['queued', 'paused'].includes(item.status))
  if (filter.value === 'completed') return items.value.filter((item) => item.status === 'completed')
  return items.value.filter((item) => ['failed', 'canceled'].includes(item.status))
})

const hasActive = computed(() => items.value.some((item) => ['queued', 'running', 'paused'].includes(item.status)))

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

function statusLabel(status) {
  return (
    {
      queued: '排队中',
      running: '下载中',
      completed: '已完成',
      failed: '失败',
      canceled: '已取消',
      paused: '已暂停',
    }[status] || status
  )
}

function statusClass(status) {
  if (status === 'completed') return 'success'
  if (status === 'running') return 'primary'
  if (status === 'failed') return 'danger'
  if (status === 'canceled' || status === 'paused') return 'warning'
  return ''
}

function cover(item) {
  return api.imageUrl(item.comicCover, item.sourceId)
}

function formatSize(bytes) {
  if (!bytes || bytes <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return String(value >= 100 || unit === 0 ? Math.round(value) : value.toFixed(1)) + ' ' + units[unit]
}

function formatTime(item) {
  const raw = item.finishedAt || item.updatedAt
  if (!raw) return '—'
  return new Date(raw).toLocaleString()
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
    <span>加载下载任务</span>
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
        <div class="download-cover">
          <img v-if="item.comicCover" :src="cover(item)" alt="" loading="lazy" />
          <span v-else class="cover-fallback"><BookOpen :size="16" /></span>
        </div>
        <div class="download-titles">
          <RouterLink
            class="comic-title"
            :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.comicId } }"
            @click.stop
          >
            {{ item.comicTitle }}
          </RouterLink>
          <span class="download-chapter">{{ item.chapterTitle }}</span>
        </div>
      </div>
      <div class="download-status">
        <span class="badge" :class="statusClass(item.status)">{{ statusLabel(item.status) }}</span>
        <span v-if="item.error" class="download-error">{{ item.error }}</span>
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
