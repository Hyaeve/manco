<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import {
  Archive,
  BookOpen,
  CalendarClock,
  FolderOpen,
  Languages,
  Loader2,
  MoreVertical,
  Pencil,
  Power,
  RefreshCw,
  Rss,
  Save,
  Trash2,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import DirectoryPicker from '../components/DirectoryPicker.vue'
import { notify } from '../stores/notices'

const items = ref([])
const jobs = ref([])
const sources = ref({})
const loading = ref(true)
const busy = ref(0)
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
      api.downloads(500),
      Object.keys(sources.value).length ? Promise.resolve({ items: [] }) : api.sources().catch(() => ({ items: [] })),
    ])
    for (const source of sourcePayload.items || []) {
      sources.value = { ...sources.value, [source.id]: source }
    }
    items.value = subscriptions.items || []
    jobs.value = downloads.items || []
  } catch (err) {
    if (!silent) notify(`订阅清单加载失败：${err.message}`, true)
  } finally {
    loading.value = false
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
    notify('Cron 表达式不能为空', true)
    return
  }
  busy.value = editing.value.id
  try {
    const updated = await api.updateSubscription(editing.value.id, {
      cronExpr: form.value.cronExpr.trim(),
      downloadDir: form.value.downloadDir.trim(),
      convertToSimplified: Boolean(form.value.convertToSimplified),
    })
    Object.assign(editing.value, updated)
    notify(`已更新《${editing.value.title}》的订阅任务`)
    editorOpen.value = false
  } catch (err) {
    notify(err.message, true)
  } finally {
    busy.value = 0
  }
}

async function toggleEnabled(item) {
  openMenu.value = 0
  busy.value = item.id
  try {
    const updated = await api.updateSubscription(item.id, { enabled: !item.enabled })
    Object.assign(item, updated)
    notify(`《${item.title}》已${item.enabled ? '启用' : '禁用'}`)
  } catch (err) {
    notify(err.message, true)
  } finally {
    busy.value = 0
  }
}

async function archive(item) {
  openMenu.value = 0
  busy.value = item.id
  try {
    await api.archiveSubscription(item.id)
    items.value = items.value.filter((row) => row.id !== item.id)
    notify(`《${item.title}》已归档`)
  } catch (err) {
    notify(err.message, true)
  } finally {
    busy.value = 0
  }
}

async function remove(item) {
  openMenu.value = 0
  if (!window.confirm(`确定删除订阅「${item.title}」吗？`)) return
  busy.value = item.id
  try {
    await api.deleteSubscription(item.id)
    items.value = items.value.filter((row) => row.id !== item.id)
    notify(`《${item.title}》已删除`)
  } catch (err) {
    notify(err.message, true)
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
  if (item.chapterCount) return item.chapterCount
  return jobs.value.filter((job) => job.sourceId === item.sourceId && job.comicId === item.comicId).length
}
</script>

<template>
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
          <BookOpen v-else :size="24" />
        </RouterLink>
        <div class="subscription-meta">
          <RouterLink
            class="subscription-title"
            :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.comicId } }"
          >
            {{ item.title }}
          </RouterLink>
          <span class="subscription-sub">{{ item.author || '未知作者' }}</span>
          <span class="subscription-sub">
            {{ chapterCount(item) }} 话 · 最新章节：{{ item.lastChapterTitle || '尚未记录' }}
          </span>
          <div class="subscription-line">
            <CalendarClock :size="14" />
            <span class="mono">{{ item.cronExpr || '未设置' }}</span>
          </div>
          <div class="subscription-line">
            <FolderOpen :size="14" />
            <span>{{ item.downloadDir || '默认下载目录' }}</span>
          </div>
        </div>
      </div>
      <div class="subscription-menu">
        <button class="btn ghost icon subscription-more" type="button" aria-label="更多操作" @click="toggleMenu(item, $event)">
          <Loader2 v-if="busy === item.id" :size="18" class="spin" />
          <MoreVertical v-else :size="18" />
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
    </article>
  </div>

  <div v-if="editorOpen" class="modal-backdrop" @click.self="editorOpen = false">
    <div class="modal">
      <div class="modal-head">
        <div>
          <h2>订阅任务设置</h2>
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
          <DirectoryPicker v-model="form.downloadDir" />
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
