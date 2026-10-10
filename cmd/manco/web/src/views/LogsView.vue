<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { Code2, Loader2, ListTree, RefreshCw, Search } from 'lucide-vue-next'
import { api } from '../api'
import RoundedSelect from '../components/RoundedSelect.vue'
import ThinScroll from '../components/ThinScroll.vue'
import { notify } from '../stores/notices'

const LOG_MODE_KEY = 'manco.logs.mode.v1'
const levels = [
  { value: 'all', label: '全部级别' },
  { value: 'info', label: '信息' },
  { value: 'warning', label: '警告' },
  { value: 'error', label: '错误' },
]

const lines = ref([])
const entries = ref([])
const loading = ref(false)
const autoRefresh = ref(true)
const mode = ref(window.localStorage.getItem(LOG_MODE_KEY) === 'structured' ? 'structured' : 'raw')
const level = ref('all')
const query = ref('')
let timer = null

const filtered = computed(() => {
  const keyword = query.value.trim().toLowerCase()
  let rows = entries.value
  if (level.value !== 'all') rows = rows.filter((entry) => entry.level === level.value)
  if (keyword) {
    rows = rows.filter((entry) => `${entry.time} ${entry.level} ${entry.message}`.toLowerCase().includes(keyword))
  }
  return rows
})

const rawText = computed(() => {
  const keyword = query.value.trim().toLowerCase()
  const rows = lines.value.filter((line) => !keyword || line.toLowerCase().includes(keyword))
  return rows.length ? rows.join('\n') : '暂无匹配日志。'
})

async function load() {
  loading.value = true
  try {
    const payload = await api.logs(500)
    lines.value = payload.lines || []
    entries.value = payload.items || []
  } catch (err) {
    notify(`运行日志加载失败：${err.message}`, true)
  } finally {
    loading.value = false
  }
}

function resetTimer() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
  if (autoRefresh.value) timer = setInterval(load, 5000)
}

watch(autoRefresh, () => {
  resetTimer()
  if (autoRefresh.value) load()
})

watch(mode, (value) => window.localStorage.setItem(LOG_MODE_KEY, value))

onMounted(() => {
  load()
  resetTimer()
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <section class="log-panel" aria-label="运行日志">
    <div class="log-toolbar">
      <div class="segmented" role="tablist" aria-label="日志视图">
        <button type="button" :class="{ active: mode === 'raw' }" @click="mode = 'raw'">
          <Code2 :size="14" />
          原始
        </button>
        <button type="button" :class="{ active: mode === 'structured' }" @click="mode = 'structured'">
          <ListTree :size="14" />
          结构化
        </button>
      </div>
      <RoundedSelect v-model="level" label="日志级别" :options="levels" />
      <label class="search-field log-search">
        <Search :size="16" />
        <input v-model="query" aria-label="搜索日志" placeholder="搜索日志..." />
      </label>
      <label class="inline small auto-refresh">
        <input v-model="autoRefresh" type="checkbox" />
        <span>自动刷新</span>
      </label>
      <button class="btn secondary small" type="button" :disabled="loading" @click="load">
        <RefreshCw :size="14" :class="{ spin: loading }" />
        刷新
      </button>
    </div>

    <div class="log-list-shell">
      <ThinScroll class="log-scroll" :thickness="1">
        <div v-if="loading && !entries.length" class="empty">
          <Loader2 :size="22" class="spin" />
          <span>加载日志</span>
        </div>
        <pre v-else-if="mode === 'raw'" class="log-view">{{ rawText }}</pre>
        <div v-else class="log-list">
          <div v-if="!filtered.length" class="empty">暂无匹配日志。</div>
          <article v-for="(entry, index) in filtered" :key="`${entry.time}-${index}`" class="log-entry">
            <time class="log-time">{{ entry.time || '--' }}</time>
            <span class="log-level" :class="entry.level">{{ entry.level }}</span>
            <span class="log-message">{{ entry.message }}</span>
          </article>
        </div>
      </ThinScroll>
    </div>
    <footer class="log-foot">{{ filtered.length }} 条记录 · 最近 500 行</footer>
  </section>
</template>
