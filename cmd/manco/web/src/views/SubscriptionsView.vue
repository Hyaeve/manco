<script setup>
import { onMounted, ref } from 'vue'
import { BookOpen, Loader2, Play, RefreshCw, Rss, Trash2 } from 'lucide-vue-next'
import { api } from '../api'

const items = ref([])
const loading = ref(true)
const busy = ref(0)
const error = ref('')
const message = ref('')

onMounted(load)

async function load() {
  loading.value = true
  try {
    const payload = await api.subscriptions()
    items.value = payload.items || []
  } catch (err) {
    error.value = err.message
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
    message.value = '已触发检查，稍后刷新即可看到新章节任务。'
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
</script>

<template>
  <div v-if="error" class="alert error">{{ error }}</div>
  <div v-if="message" class="alert ok">{{ message }}</div>
  <div v-if="loading" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载订阅</span>
  </div>
  <div v-else-if="!items.length" class="card empty">
    <Rss :size="26" />
    <p>还没有订阅。在发现页打开作品后即可订阅追更。</p>
  </div>
  <div v-else class="card table-wrap">
    <table>
      <thead>
        <tr>
          <th>作品</th>
          <th>最新章节</th>
          <th>自动下载</th>
          <th>启用</th>
          <th>最近检查</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="item in items" :key="item.id">
          <td>
            <div class="inline">
              <img
                v-if="item.cover"
                :src="cover(item)"
                alt=""
                style="width: 34px; height: 48px; object-fit: cover; border-radius: 4px"
              />
              <span v-else class="cover-fallback" style="width: 34px; height: 48px">
                <BookOpen :size="16" />
              </span>
              <span>
                <RouterLink
                  class="comic-title"
                  :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.comicId } }"
                >
                  {{ item.title }}
                </RouterLink>
                <span class="muted small" style="display: block">{{ item.sourceId }}</span>
              </span>
            </div>
          </td>
          <td class="muted">{{ item.lastChapterTitle || '尚未记录' }}</td>
          <td>
            <input
              type="checkbox"
              :checked="item.autoDownload"
              :disabled="busy === item.id"
              @change="toggleAuto(item, $event.target.checked)"
            />
          </td>
          <td>
            <input
              type="checkbox"
              :checked="item.enabled"
              :disabled="busy === item.id"
              @change="toggleEnabled(item, $event.target.checked)"
            />
          </td>
          <td class="muted small">
            {{ item.lastCheckedAt ? new Date(item.lastCheckedAt).toLocaleString() : '从未' }}
          </td>
          <td>
            <div class="inline">
              <button class="btn secondary small" type="button" :disabled="busy === item.id" @click="check(item)">
                <Loader2 v-if="busy === item.id" :size="14" class="spin" />
                <Play v-else :size="14" />
                检查
              </button>
              <button class="btn danger small" type="button" :disabled="busy === item.id" @click="remove(item)">
                <Trash2 :size="14" />
              </button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
  <div class="inline" style="margin-top: 14px">
    <button class="btn secondary small" type="button" @click="load">
      <RefreshCw :size="15" />
      刷新
    </button>
  </div>
</template>
