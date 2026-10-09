<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { Code2, ListTree, Loader2, RefreshCw, ScrollText } from 'lucide-vue-next'
import { api } from '../api'

const LOG_MODE_KEY = 'manco.logs.mode.v1'

const lines = ref([])
const entries = ref([])
const loading = ref(true)
const error = ref('')
const autoRefresh = ref(true)
const mode = ref(window.localStorage.getItem(LOG_MODE_KEY) === 'structured' ? 'structured' : 'raw')
const scroller = ref(null)
let timer = null

async function load() {
  error.value = ''
  try {
    const payload = await api.logs(500)
    lines.value = payload.lines || []
    entries.value = payload.items || lines.value.map((line) => ({ level: 'info', message: line, raw: line, time: '' }))
    await nextTick()
    scrollToBottom()
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

function scrollToBottom() {
  const element = scroller.value
  if (element) element.scrollTop = element.scrollHeight
}

function resetTimer() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
  if (autoRefresh.value) {
    timer = setInterval(load, 5000)
  }
}

watch(autoRefresh, () => {
  resetTimer()
  if (autoRefresh.value) load()
})

watch(mode, (value) => {
  window.localStorage.setItem(LOG_MODE_KEY, value)
})

onMounted(() => {
  load()
  resetTimer()
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})

const hasLines = computed(() => lines.value.length > 0)
</script>

<template>
  <div v-if="error" class="alert error">{{ error }}</div>
  <div class="card card-pad">
    <div class="section-head" style="margin-bottom: 10px">
      <div class="inline">
        <ScrollText :size="17" />
        <h2>系统日志</h2>
      </div>
      <div class="inline">
        <div class="segmented">
          <button type="button" :class="{ active: mode === 'raw' }" @click="mode = 'raw'">
            <Code2 :size="14" />
            原始
          </button>
          <button type="button" :class="{ active: mode === 'structured' }" @click="mode = 'structured'">
            <ListTree :size="14" />
            结构化
          </button>
        </div>
        <label class="inline small">
          <input v-model="autoRefresh" type="checkbox" />
          <span>自动刷新</span>
        </label>
        <button class="btn secondary small" type="button" :disabled="loading" @click="load">
          <RefreshCw :size="14" :class="{ spin: loading }" />
          刷新
        </button>
      </div>
    </div>

    <div v-if="loading && !hasLines" class="empty">
      <Loader2 :size="22" class="spin" />
      <span>加载日志</span>
    </div>
    <pre v-else-if="mode === 'raw'" ref="scroller" class="log-view">{{ hasLines ? lines.join('\n') : '暂无日志。' }}</pre>
    <div v-else ref="scroller" class="log-list">
      <div v-if="!entries.length" class="empty">暂无日志。</div>
      <article v-for="(entry, index) in entries" :key="`${entry.time}-${index}`" class="log-entry">
        <time class="log-time">{{ entry.time || '--' }}</time>
        <span class="log-level" :class="entry.level">{{ entry.level }}</span>
        <span class="log-message">{{ entry.message }}</span>
      </article>
    </div>
  </div>
</template>
