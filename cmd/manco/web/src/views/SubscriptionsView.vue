<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import {
  BookOpen,
  CircleCheck,
  Clock,
  Loader2,
  Play,
  RefreshCw,
  Rss,
  Trash2,
} from 'lucide-vue-next'
import { api } from '../api'

const items = ref([])
const jobs = ref([])
const loading = ref(true)
const busy = ref(0)
const error = ref('')
const message = ref('')
let timer = 0

const hasActive = computed(() => jobs.value.some((job) => ['queued', 'running', 'paused'].includes(job.status)))

onMounted(async () => {
  await load()
  timer = window.setInterval(() => {
    if (hasActive.value) load(true)
  }, 5000)
})

onUnmounted(() => {
  if (timer) window.clearInterval(timer)
})

async function load(silent = false) {
  if (!silent) loading.value = true
  try {
    const [subscriptions, downloads] = await Promise.all([api.subscriptions(), api.downloads(300)])
    items.value = subscriptions.items || []
    jobs.value = downloads.items || []
    if (silent) error.value = ''
  } catch (err) {
    if (!silent) error.value = err.message
  } finally {
    loading.value = false
  }
}

async function toggleAuto(item, value) {
  busy.value = item.id
  try {
    const updated = await api.updateSubscription(item.id, { autoDownload: value })
    Object.assign(item, updated)
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = 0
  }
}

async function toggleEnabled(item, value) {
  busy.value = item.id
  try {
    const updated = await api.updateSubscription(item.id, { enabled: value })
    Object.assign(item, updated)
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = 0
  }
}

async function check(item) {
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
  if (!window.confirm(`确定删除订阅「${item.title}」吗？`)) return
  busy.value = item.id
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

function stats(item) {
  const rows = jobs.value.filter((job) => job.sourceId === item.sourceId && job.comicId === item.comicId)
  const active = rows.filter((job) => ['queued', 'running', 'paused'].includes(job.status)).length
  const completed = rows.filter((job) => job.status === 'completed').length
  const failed = rows.filter((job) => ['failed', 'canceled'].includes(job.status)).length
  return { active, completed, failed }
}

function updatedText(item) {
  return item.lastCheckedAt ? `最近检查 ${new Date(item.lastCheckedAt).toLocaleString()}` : '尚未检查'
}
</script>

<template>
  <div v-if="error" class="alert error">{{ error }}</div>
  <div v-if="message" class="alert ok">{{ message }}</div>

  <div v-if="loading && !items.length" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载订阅</span>
  </div>
  <div v-else-if="!items.length" class="card empty">
    <Rss :size="26" />
    <p>还没有订阅。在发现页打开作品后即可订阅追更。</p>
    <RouterLink class="btn small" to="/discover">去发现页</RouterLink>
  </div>

  <div v-else class="subscription-grid">
    <article v-for="item in items" :key="item.id" class="subscription-card">
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
          <span class="subscription-sub">{{ item.author || '未知作者' }} · {{ item.sourceId }}</span>
          <span class="subscription-sub">最新章节：{{ item.lastChapterTitle || '尚未记录' }}</span>
          <span class="subscription-sub inline">
            <Clock :size="13" />
            {{ updatedText(item) }}
          </span>
          <div class="inline">
            <span class="badge primary">进行中 {{ stats(item).active }}</span>
            <span class="badge success">
              <CircleCheck :size="12" />
              已完成 {{ stats(item).completed }}
            </span>
            <span v-if="stats(item).failed" class="badge danger">失败 {{ stats(item).failed }}</span>
          </div>
        </div>
      </div>
      <div class="subscription-actions">
        <div class="subscription-toggles">
          <label>
            <input
              type="checkbox"
              :checked="item.enabled"
              :disabled="busy === item.id"
              @change="toggleEnabled(item, $event.target.checked)"
            />
            订阅
          </label>
          <label>
            <input
              type="checkbox"
              :checked="item.autoDownload"
              :disabled="busy === item.id"
              @change="toggleAuto(item, $event.target.checked)"
            />
            自动下载
          </label>
        </div>
        <span class="spacer" />
        <button class="btn secondary small" type="button" :disabled="busy === item.id" @click="check(item)">
          <Loader2 v-if="busy === item.id" :size="14" class="spin" />
          <Play v-else :size="14" />
          检查更新
        </button>
        <button class="btn danger small" type="button" :disabled="busy === item.id" @click="remove(item)">
          <Trash2 :size="14" />
        </button>
      </div>
    </article>
  </div>

  <div class="inline" style="margin-top: 14px">
    <button class="btn secondary small" type="button" @click="load()">
      <RefreshCw :size="15" />
      刷新
    </button>
  </div>
</template>
