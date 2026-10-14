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
  { value: 'debug', label: '调试' },
]
const modules = [
  { value: 'all', label: '全部模块' },
  { value: 'system', label: '系统' },
  { value: 'accounts', label: '账号安全' },
  { value: 'sources', label: '资源源' },
  { value: 'repositories', label: '拓展仓库' },
  { value: 'subscriptions', label: '订阅' },
  { value: 'downloads', label: '下载' },
  { value: 'local', label: '本地库' },
  { value: 'settings', label: '系统设置' },
]

const entries = ref([])
const loading = ref(false)
const mode = ref(window.localStorage.getItem(LOG_MODE_KEY) === 'structured' ? 'structured' : 'raw')
const level = ref('all')
const module = ref('all')
const query = ref('')
let timer = null

const filtered = computed(() => {
  const keyword = query.value.trim().toLowerCase()
  let rows = entries.value
  if (module.value !== 'all') rows = rows.filter((entry) => entry.module === module.value)
  if (level.value !== 'all') rows = rows.filter((entry) => entry.level === level.value)
  if (keyword) {
    rows = rows.filter((entry) => `${entry.time} ${entry.module} ${entry.level} ${entry.message}`.toLowerCase().includes(keyword))
  }
  return rows
})


const levelLabels = { error: '错误', warning: '警告', info: '信息', debug: '调试' }
function levelLabel(level) {
  return levelLabels[level] || '信息'
}

async function load() {
  loading.value = true
  try {
    const payload = await api.logs(500)
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
  // 运行日志默认且始终自动刷新，不再提供关闭开关。
  timer = setInterval(load, 5000)
}

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
      <RoundedSelect v-model="module" label="日志模块" :options="modules" />
      <RoundedSelect v-model="level" label="日志级别" :options="levels" />
      <button class="btn secondary small" type="button" :disabled="loading" @click="load">
        <RefreshCw :size="14" :class="{ spin: loading }" />
        刷新
      </button>
      <label class="search-field log-search">
        <Search :size="16" />
        <input v-model="query" aria-label="搜索日志" placeholder="搜索日志..." />
      </label>
    </div>

    <div class="log-list-shell">
      <ThinScroll class="log-scroll" :thickness="1">
        <div v-if="loading && !entries.length" class="empty">
          <Loader2 :size="22" class="spin" />
          <span>加载日志</span>
        </div>
        <div v-else-if="mode === 'raw'" class="log-view log-raw">
          <div v-if="!filtered.length" class="empty">暂无匹配日志。</div>
          <div
            v-for="(entry, index) in filtered"
            :key="`raw-${entry.time}-${index}`"
            class="log-raw-line"
            :class="`is-${entry.level}`"
          >
            <span class="log-raw-marker" :class="`is-${entry.level}`">{{ levelLabel(entry.level) }}</span>
            <span class="log-raw-text">
              <time v-if="entry.time" class="log-time">{{ entry.time }}</time>
              <span class="log-raw-message">{{ entry.message }}</span>
            </span>
          </div>
        </div>
        <div v-else class="log-list">
          <div v-if="!filtered.length" class="empty">暂无匹配日志。</div>
          <article v-for="(entry, index) in filtered" :key="`${entry.time}-${index}`" class="log-entry">
            <time class="log-time">{{ entry.time || '--' }}</time>
            <span class="log-module">{{ entry.module || 'system' }}</span>
            <span class="log-level" :class="entry.level">{{ levelLabel(entry.level) }}</span>
            <span class="log-message">{{ entry.message }}</span>
          </article>
        </div>
      </ThinScroll>
    </div>
    <footer class="log-foot">{{ filtered.length }} 条记录 · 最近 500 行</footer>
  </section>
</template>
