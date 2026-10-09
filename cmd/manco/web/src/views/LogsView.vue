<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { Loader2, RefreshCw, ScrollText } from 'lucide-vue-next'
import { api } from '../api'

const lines = ref([])
const loading = ref(true)
const error = ref('')
const autoRefresh = ref(true)
const scroller = ref(null)
let timer = null

async function load() {
  error.value = ''
  try {
    const payload = await api.logs(500)
    lines.value = payload.lines || []
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
    <p class="muted small" style="margin: 0 0 12px">
      最多保留最近 500 行；容器日志同样可用 <code>docker logs -f Manco</code> 查看。
    </p>
    <div v-if="loading && !hasLines" class="empty">
      <Loader2 :size="22" class="spin" />
      <span>加载日志</span>
    </div>
    <pre v-else ref="scroller" class="log-view">{{ hasLines ? lines.join('\n') : '暂无日志。' }}</pre>
  </div>
</template>
