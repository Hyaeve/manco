<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import {
  Archive,
  BookOpen,
  CalendarClock,
  CircleCheck,
  Download,
  FolderOpen,
  Languages,
  Loader2,
  MoreVertical,
  Pencil,
  Play,
  Power,
  RefreshCw,
  Rss,
  Save,
  Trash2,
  X,
} from 'lucide-vue-next'
import { api } from '../api'

const items = ref([])
const jobs = ref([])
const sources = ref({})
const directories = ref([])
const loading = ref(true)
const busy = ref(0)
const error = ref('')
const message = ref('')
const openMenu = ref(0)
const editorOpen = ref(false)
const editing = ref(null)
let timer = 0

const form = ref({
  cronExpr: '',
  downloadDir: '',
  convertToSimplified: false,
})

const hasActive = computed(() => jobs.value.some((job) => ['queued', 'running', 'paused'].includes(job.status)))

onMounted(async () => {
  await load()
  await loadDirectories()
  timer = window.setInterval(() => {
    if (hasActive.value) load(true)
  }, 5000)
  window.addEventListener('click', closeMenus)
})

onUnmounted(() => {
  if (timer) window.clearInterval(timer)
  window.removeEventListener('click', closeMenus)
})

function closeMenus(event) {
  if (event && event.target.closest('.subscription-menu')) return
  openMenu.value = 0
}

async function load(silent = false) {
  if (!silent) loading.value = true
  try {
    const [subscriptions, downloads, sourcePayload] = await Promise.all([
      api.subscriptions(),
      api.downloads(300),
      Object.keys(sources.value).length ? Promise.resolve({ items: [] }) : api.sources().catch(() => ({ items: [] })),
    ])
    for (const source of sourcePayload.items || []) {
      sources.value = { ...sources.value, [source.id]: source }
    }
    items.value = subscriptions.items || []
    jobs.value = downloads.items || []
    if (silent) error.value = ''
  } catch (err) {
    if (!silent) error.value = err.message
  } finally {
    loading.value = false
  }
}

async function loadDirectories() {
  try {
    const payload = await api.downloadDirectories()
    directories.value = payload.items || []
  } catch {
    directories.value = []
  }
}

function toggleMenu(item, event) {
  event.stopPropagation()
  openMenu.value = openMenu.value === item.id ? 0 : item.id
}

function openEditor(item) {
  editing.value = item
  form.value = {
    cronExpr: item.cronExpr || '',
    downloadDir: item.downloadDir || '',
    convertToSimplified: Boolean(item.convertToSimplified),
  }
  openMenu.value = 0
  editorOpen.value = true
}

async function saveEditor() {
  if (!editing.value) return
  if (!form.value.cronExpr.trim()) {
    error.value = 'Cron 表达式不能为空'
    return
  }
  busy.value = editing.value.id
  error.value = ''
  message.value = ''
  try {
    const updated = await api.updateSubscription(editing.value.id, {
      cronExpr: form.value.cronExpr.trim(),
      downloadDir: form.value.downloadDir.trim(),
      convertToSimplified: Boolean(form.value.convertToSimplified),
    })
    Object.assign(editing.value, updated)
    message.value = `已更新《${editing.value.title}》的订阅设置。`
    editorOpen.value = false
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = 0
  }
}

async function toggleEnabled(item) {
  openMenu.value = 0
  busy.value = item.id
  error.value = ''
  message.value = ''
  try {
    const updated = await api.updateSubscription(item.id, { enabled: !item.enabled })
    Object.assign(item, updated)
    message.value = `《${item.title}》已${item.enabled ? '启用' : '停用'}。`
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = 0
  }
}

async function archive(item) {
  openMenu.value = 0
  busy.value = item.id
  error.value = ''
  message.value = ''
  try {
    await api.archiveSubscription(item.id)
    items.value = items.value.filter((row) => row.id !== item.id)
    message.value = `《${item.title}》已归档。`
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = 0
  }
}

async function download(item) {
  openMenu.value = 0
  busy.value = item.id
  message.value = ''
  error.value = ''
  try {
    const result = await api.downloadSubscription(item.id)
    message.value =
      result.item?.status === 'completed'
        ? `《${item.title}》最新章节已在本地。`
        : `已将《${item.title}》最新章节加入下载队列。`
    await load(true)
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = 0
  }
}

async function check(item) {
  openMenu.value = 0
  busy.value = item.id
  message.value = ''
  error.value = ''
  try {
    await api.checkSubscription(item.id)
    message.value = `已检查《${item.title}》，新章节会自动加入下载队列。`
    await load(true)
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = 0
  }
}

async function remove(item) {
  openMenu.value = 0
  if (!window.confirm(`确定删除订阅「${item.title}」吗？`)) return
  busy.value = item.id
  error.value = ''
  try {
    await api.deleteSubscription(item.id)
    items.value = items.value.filter((row) => row.id !== item.id)
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = 0
  }
}

function cover(item) {
  return api.imageUrl(item.cover, item.sourceId)
}

function sourceIcon(item) {
  return sources.value[item.sourceId]?.icon || ''
}

function chapterCount(item) {
  return jobs.value.filter((job) => job.sourceId === item.sourceId && job.comicId === item.comicId).length
}

function stats(item) {
  const rows = jobs.value.filter((job) => job.sourceId === item.sourceId && job.comicId === item.comicId)
  const active = rows.filter((job) => ['queued', 'running', 'paused'].includes(job.status)).length
  const completed = rows.filter((job) => job.status === 'completed').length
  const failed = rows.filter((job) => ['failed', 'canceled'].includes(job.status)).length
  return { active, completed, failed }
}
</script>

<template>
  <div v-if="error" class="alert error">{{ error }}</div>
  <div v-if="message" class="alert ok">{{ message }}</div>

  <div class="toolbar subscription-toolbar">
    <span class="spacer" />
    <button class="btn secondary small" type="button" :disabled="loading" @click="load()">
      <RefreshCw :size="15" :class="{ spin: loading }" />
      刷新
    </button>
  </div>

  <div v-if="loading && !items.length" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载订阅清单</span>
  </div>
  <div v-else-if="!items.length" class="card empty">
    <Rss :size="26" />
    <p>还没有订阅。在探索发现打开作品后即可订阅追更。</p>
    <RouterLink class="btn small" to="/discover">去探索发现</RouterLink>
  </div>

  <div v-else class="subscription-grid">
    <article v-for="item in items" :key="item.id" class="subscription-card" :class="{ disabled: !item.enabled }">
      <span v-if="sourceIcon(item)" class="subscription-source" :title="item.sourceId">
        <img :src="sourceIcon(item)" :alt="item.sourceId" loading="lazy" />
      </span>
      <div class="subscription-main">
        <RouterLink
          class="subscription-cover"
          :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.comicId } }"
        >
          <img v-if="item.cover" :src="cover(item)" :alt="item.title" loading="lazy" />
          <BookOpen v-else :size="22" />
        </RouterLink>
        <div class="subscription-meta">
          <RouterLink
            class="subscription-title"
            :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.comicId } }"
          >
            {{ item.title }}
          </RouterLink>
          <span class="subscription-sub">{{ item.author || '未知作者' }}</span>
          <span class="subscription-sub">已记录 {{ chapterCount(item) }} 话 · 最新章节：{{ item.lastChapterTitle || '尚未记录' }}</span>
          <div class="subscription-line">
            <CalendarClock :size="13" />
            <span class="mono">{{ item.cronExpr || '未设置' }}</span>
          </div>
          <div class="subscription-line">
            <FolderOpen :size="13" />
            <span>{{ item.downloadDir || '默认下载目录' }}</span>
          </div>
          <span v-if="item.convertToSimplified" class="badge"><Languages :size="12" /> 繁转简</span>
          <div class="inline">
            <span class="badge primary">进行中 {{ stats(item).active }}</span>
            <span class="badge success"><CircleCheck :size="12" /> 已完成 {{ stats(item).completed }}</span>
            <span v-if="stats(item).failed" class="badge danger">失败 {{ stats(item).failed }}</span>
            <span v-if="!item.enabled" class="badge warning">已停用</span>
          </div>
        </div>
      </div>
      <div class="subscription-menu">
        <button class="btn ghost icon" type="button" aria-label="更多操作" @click="toggleMenu(item, $event)">
          <MoreVertical :size="18" />
        </button>
        <div v-if="openMenu === item.id" class="dropdown-panel subscription-actions" @click.stop>
          <button class="dropdown-item" type="button" @click="openEditor(item)">
            <Pencil :size="15" /> 编辑
          </button>
          <button class="dropdown-item" type="button" @click="toggleEnabled(item)">
            <Power :size="15" /> {{ item.enabled ? '禁用' : '启用' }}
          </button>
          <button class="dropdown-item" type="button" @click="archive(item)">
            <Archive :size="15" /> 归档
          </button>
          <button class="dropdown-item danger" type="button" @click="remove(item)">
            <Trash2 :size="15" /> 删除
          </button>
        </div>
      </div>
      <div class="subscription-quick">
        <button class="btn primary small" type="button" :disabled="busy === item.id" @click="download(item)">
          <Loader2 v-if="busy === item.id" :size="14" class="spin" />
          <Download v-else :size="14" />
          下载最新话
        </button>
        <button class="btn secondary small" type="button" :disabled="busy === item.id" @click="check(item)">
          <Play :size="14" />
          检查更新
        </button>
      </div>
    </article>
  </div>

  <div v-if="editorOpen" class="modal-backdrop" @click.self="editorOpen = false">
    <div class="modal">
      <div class="modal-head">
        <div>
          <h2>编辑订阅任务</h2>
          <p class="muted small">{{ editing?.title }}</p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="editorOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <label class="field">
          <span class="inline"><CalendarClock :size="14" /> Cron 检查周期</span>
          <input v-model="form.cronExpr" class="input" placeholder="0 21 * * 5" />
        </label>
        <label class="field">
          <span class="inline"><FolderOpen :size="14" /> 下载位置</span>
          <select v-model="form.downloadDir" class="input">
            <option value="">使用默认下载目录</option>
            <option v-for="dir in directories" :key="dir.path" :value="dir.path">
              {{ dir.path }}{{ dir.default ? '（默认）' : '' }}
            </option>
          </select>
          <input v-model="form.downloadDir" class="input" placeholder="也可手动填写容器内目录" style="margin-top: 8px" />
        </label>
        <label class="switch-row">
          <input v-model="form.convertToSimplified" type="checkbox" />
          <span><Languages :size="14" /> 下载时执行繁体转简体</span>
        </label>
      </div>
      <div class="modal-foot">
        <button class="btn secondary" type="button" @click="editorOpen = false">取消</button>
        <button class="btn" type="button" :disabled="busy === editing?.id" @click="saveEditor">
          <Loader2 v-if="busy === editing?.id" :size="15" class="spin" />
          <Save v-else :size="15" />
          保存
        </button>
      </div>
    </div>
  </div>
</template>
