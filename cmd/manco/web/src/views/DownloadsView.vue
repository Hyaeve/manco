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
} from 'lucide-vue-next'
import { api } from '../api'

const items = ref([])
const loading = ref(true)
const error = ref('')
const message = ref('')
const filter = ref('all')
const busy = ref(0)
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

onMounted(async () => {
  await load()
  timer = window.setInterval(() => {
    if (hasActive.value) load(true)
  }, 4000)
})

onUnmounted(() => {
  if (timer) window.clearInterval(timer)
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

function percent(item) {
  if (!item.totalPages) return 0
  return Math.min(100, Math.round((item.completedPages / item.totalPages) * 100))
}

async function retry(item) {
  busy.value = item.id
  error.value = ''
  message.value = ''
  try {
    await api.retryDownload(item.id)
    message.value = '任务已重新加入队列。'
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
  if (!window.confirm(hint)) return
  busy.value = item.id
  error.value = ''
  try {
    await api.deleteDownload(item.id, removeFile)
    items.value = items.value.filter((row) => row.id !== item.id)
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = 0
  }
}
</script>

<template>
  <div v-if="error" class="alert error">{{ error }}</div>
  <div v-if="message" class="alert ok">{{ message }}</div>

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
    <RouterLink class="btn secondary small" to="/discover">
      <Compass :size="15" />
      去发现页新建下载
    </RouterLink>
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
  <div v-else class="card table-wrap">
    <table>
      <thead>
        <tr>
          <th>作品 / 章节</th>
          <th>状态</th>
          <th>进度</th>
          <th>输出文件</th>
          <th>更新时间</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="item in visible" :key="item.id">
          <td>
            <div class="inline">
              <img
                v-if="item.comicCover"
                :src="cover(item)"
                alt=""
                style="width: 34px; height: 48px; object-fit: cover; border-radius: 4px"
              />
              <span v-else class="cover-fallback" style="width: 34px; height: 48px; position: static">
                <BookOpen :size="16" />
              </span>
              <span>
                <RouterLink
                  class="comic-title"
                  :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.comicId } }"
                >
                  {{ item.comicTitle }}
                </RouterLink>
                <span class="muted small" style="display: block">{{ item.chapterTitle }}</span>
              </span>
            </div>
          </td>
          <td>
            <span class="badge" :class="statusClass(item.status)">{{ statusLabel(item.status) }}</span>
            <span v-if="item.error" class="muted small" style="display: block; max-width: 220px">
              {{ item.error }}
            </span>
          </td>
          <td style="min-width: 150px">
            <div class="progress" v-if="item.totalPages">
              <span :style="{ width: `${percent(item)}%` }" />
            </div>
            <span class="muted small">
              {{ item.completedPages }} / {{ item.totalPages || '?' }} 页
              <template v-if="item.status === 'completed'"> · CBZ</template>
            </span>
          </td>
          <td class="muted small" style="max-width: 260px; word-break: break-all">
            <span v-if="item.filePath">{{ item.filePath }}</span>
            <span v-else>—</span>
          </td>
          <td class="muted small">{{ new Date(item.updatedAt).toLocaleString() }}</td>
          <td>
            <div class="inline">
              <button
                v-if="['failed', 'canceled', 'paused'].includes(item.status)"
                class="btn secondary small"
                type="button"
                :disabled="busy === item.id"
                @click="retry(item)"
              >
                <RotateCcw :size="14" />
                重试
              </button>
              <button
                v-if="item.status === 'completed' && item.filePath"
                class="btn secondary small"
                type="button"
                :disabled="busy === item.id"
                title="删除任务记录和已下载的 CBZ 文件"
                @click="remove(item, true)"
              >
                <FolderOpen :size="14" />
                删除文件
              </button>
              <button
                class="btn danger small"
                type="button"
                :disabled="busy === item.id"
                title="只删除任务记录"
                @click="remove(item, false)"
              >
                <Trash2 :size="14" />
              </button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
