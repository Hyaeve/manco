<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { BookOpen, ImageOff, Loader2, Search, X } from 'lucide-vue-next'
import { api } from '../api'

const route = useRoute()
const router = useRouter()
const query = ref(String(route.query.q || ''))
const items = ref([])
const errors = ref([])
const loading = ref(false)
const history = ref([])

const hasQuery = computed(() => query.value.trim().length > 0)

function loadHistory() {
  try {
    const value = JSON.parse(localStorage.getItem('manco.search.history.v1') || '[]')
    history.value = Array.isArray(value) ? value.slice(0, 3) : []
  } catch {
    history.value = []
  }
}

async function runSearch(value = query.value) {
  const keyword = String(value || '').trim()
  if (!keyword) {
    items.value = []
    loading.value = false
    return
  }
  query.value = keyword
  loading.value = true
  errors.value = []
  try {
    const payload = await api.globalSearch(keyword)
    items.value = payload.items || []
    errors.value = payload.errors || []
  } catch (err) {
    items.value = []
    errors.value = [err.message]
  } finally {
    loading.value = false
  }
}

function close() {
  router.push({ name: 'discover' })
}

function selectHistory(value) {
  query.value = value
  runSearch(value)
  router.replace({ name: 'search', query: { q: value } })
}

function cover(item) {
  return api.imageUrl(item.cover, item.sourceId)
}

onMounted(() => {
  loadHistory()
  runSearch()
})

watch(
  () => route.query.q,
  (value) => {
    query.value = String(value || '')
    runSearch()
    loadHistory()
  },
)
</script>

<template>
  <section class="search-page">
    <header class="search-page-head">
      <div class="search-page-title">
        <Search :size="20" />
        <div>
          <h1>搜索</h1>
          <p>跨所有已启用来源查找作品</p>
        </div>
      </div>
      <button class="btn icon" type="button" aria-label="退出搜索" @click="close">
        <X :size="20" />
      </button>
    </header>

    <div v-if="!hasQuery" class="search-history">
      <span class="muted small">最近搜索</span>
      <div class="inline">
        <button v-for="item in history" :key="item" class="badge" type="button" @click="selectHistory(item)">
          {{ item }}
        </button>
        <span v-if="!history.length" class="muted small">还没有搜索记录</span>
      </div>
    </div>

    <div v-if="loading" class="empty">
      <Loader2 :size="22" class="spin" />
      <span>正在搜索...</span>
    </div>
    <div v-else-if="!items.length" class="card empty">
      <ImageOff :size="26" />
      <p>{{ errors.length ? errors.join('；') : '没有找到匹配的作品' }}</p>
    </div>
    <div v-else class="search-results-grid">
      <RouterLink
        v-for="item in items"
        :key="`${item.sourceId}-${item.id}`"
        class="comic-card search-result-card"
        :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.id } }"
      >
        <div class="comic-cover">
          <img v-if="item.cover" :src="cover(item)" :alt="item.title" loading="lazy" decoding="async" />
          <span v-else class="cover-fallback"><BookOpen :size="24" /></span>
          <img v-if="item.sourceIcon" class="search-result-source" :src="item.sourceIcon" alt="" />
        </div>
        <div class="comic-body">
          <span class="comic-title">{{ item.title }}</span>
          <span class="comic-meta">{{ item.sourceName || item.sourceId }}</span>
        </div>
      </RouterLink>
    </div>
  </section>
</template>
