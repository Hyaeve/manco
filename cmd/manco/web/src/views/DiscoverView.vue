<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { BookOpen, ChevronLeft, ChevronRight, ImageOff, Loader2, Search } from 'lucide-vue-next'
import { api } from '../api'

const route = useRoute()
const router = useRouter()
const sources = ref([])
const sourceId = ref('')
const query = ref('')
const page = ref(1)
const items = ref([])
const hasMore = ref(false)
const loading = ref(false)
const error = ref('')
const mode = ref('browse')

const currentSource = computed(() => sources.value.find((item) => item.id === sourceId.value) || null)

onMounted(async () => {
  try {
    const payload = await api.sources()
    sources.value = payload.items || []
    const requested = typeof route.query.source === 'string' ? route.query.source : ''
    sourceId.value = sources.value.some((item) => item.id === requested)
      ? requested
      : sources.value[0]?.id || ''
    await load()
  } catch (err) {
    error.value = err.message
  }
})

watch(sourceId, async () => {
  page.value = 1
  query.value = ''
  mode.value = 'browse'
  await load()
})

watch(
  () => route.query.source,
  (value) => {
    if (typeof value === 'string' && value && value !== sourceId.value) {
      sourceId.value = value
    }
  },
)

async function load() {
  if (!sourceId.value) return
  loading.value = true
  error.value = ''
  try {
    const result =
      mode.value === 'search' && query.value.trim()
        ? await api.search(sourceId.value, query.value.trim(), page.value)
        : await api.browse(sourceId.value, '', page.value)
    items.value = result.items || []
    hasMore.value = Boolean(result.hasMore)
  } catch (err) {
    items.value = []
    hasMore.value = false
    error.value = err.message
  } finally {
    loading.value = false
  }
}

async function submitSearch() {
  if (!query.value.trim()) {
    mode.value = 'browse'
    page.value = 1
    await load()
    return
  }
  mode.value = 'search'
  page.value = 1
  await load()
}

async function changePage(delta) {
  const next = page.value + delta
  if (next < 1) return
  page.value = next
  await load()
}

function cover(item) {
  return api.imageUrl(item.cover, item.sourceId)
}

function openQuery() {
  router.replace({ name: 'discover', query: sourceId.value ? { source: sourceId.value } : {} })
}
</script>

<template>
  <div class="toolbar">
    <label class="field" style="flex: 0 0 180px">
      <span>漫画源</span>
      <select v-model="sourceId" class="select" @change="openQuery">
        <option v-for="item in sources" :key="item.id" :value="item.id">{{ item.name }}</option>
      </select>
    </label>
    <label class="field search-input">
      <span>搜索关键词</span>
      <input
        v-model="query"
        class="input"
        placeholder="输入漫画名后回车"
        @keyup.enter="submitSearch"
      />
    </label>
    <div class="field">
      <span>&nbsp;</span>
      <button class="btn" type="button" :disabled="loading || !sourceId" @click="submitSearch">
        <Loader2 v-if="loading" :size="16" class="spin" />
        <Search v-else :size="16" />
        搜索
      </button>
    </div>
  </div>

  <div v-if="error" class="alert error">{{ error }}</div>
  <div v-else-if="currentSource && !currentSource.canSearch" class="alert info">
    当前漫画源不支持搜索，已切换为浏览模式。
  </div>

  <div v-if="!loading && !items.length" class="card empty">
    <ImageOff :size="26" />
    <p>没有找到作品，换个关键词或漫画源试试。</p>
  </div>

  <div v-else class="comic-grid">
    <RouterLink
      v-for="item in items"
      :key="`${item.sourceId}-${item.id}`"
      class="comic-card"
      :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.id } }"
    >
      <div class="comic-cover">
        <img v-if="item.cover" :src="cover(item)" :alt="item.title" loading="lazy" />
        <span v-else class="cover-fallback"><BookOpen :size="28" /></span>
      </div>
      <div class="comic-body">
        <span class="comic-title">{{ item.title }}</span>
        <span class="comic-meta">{{ item.author || '未知作者' }}</span>
      </div>
    </RouterLink>
  </div>

  <div class="inline" style="justify-content: center; margin-top: 20px">
    <button class="btn secondary small" type="button" :disabled="page <= 1 || loading" @click="changePage(-1)">
      <ChevronLeft :size="15" />
      上一页
    </button>
    <span class="muted small">第 {{ page }} 页</span>
    <button class="btn secondary small" type="button" :disabled="!hasMore || loading" @click="changePage(1)">
      下一页
      <ChevronRight :size="15" />
    </button>
  </div>
</template>
