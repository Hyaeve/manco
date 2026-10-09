<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { BookOpen, ChevronLeft, ChevronRight, ImageOff, Loader2, Search } from 'lucide-vue-next'
import { api } from '../api'

const FILTER_STORAGE_KEY = 'manco.discover.filters.v1'

const route = useRoute()
const router = useRouter()
const allSources = ref([])
const activeId = ref('')
const panels = ref({})
const loadingSources = ref(true)
const sourcesError = ref('')
const savedFilters = ref(readSavedFilters())

const sources = computed(() => allSources.value.filter((source) => !source.hidden))

function readSavedFilters() {
  try {
    const value = JSON.parse(window.localStorage.getItem(FILTER_STORAGE_KEY) || '{}')
    return value && typeof value === 'object' ? value : {}
  } catch {
    return {}
  }
}

function persistFilters() {
  window.localStorage.setItem(FILTER_STORAGE_KEY, JSON.stringify(savedFilters.value))
}

function sourceFilters(source) {
  const stored = savedFilters.value?.[source.id] || {}
  const values = {}
  for (const group of source.filters || []) {
    const candidate = stored[group.key]
    const available = (group.options || []).some((option) => String(option.value) === String(candidate))
    values[group.key] = available
      ? String(candidate)
      : String(group.default ?? group.options?.[0]?.value ?? '')
  }
  return values
}

function panelFor(source) {
  if (!panels.value[source.id]) {
    panels.value[source.id] = {
      query: '',
      mode: 'browse',
      page: 1,
      items: [],
      hasMore: false,
      loading: false,
      loaded: false,
      error: '',
      filters: sourceFilters(source),
    }
  }
  return panels.value[source.id]
}

const activeSource = computed(() => sources.value.find((source) => source.id === activeId.value) || null)
const activePanel = computed(() => (activeSource.value ? panelFor(activeSource.value) : null))

async function loadSources() {
  try {
    const payload = await api.sources()
    allSources.value = payload.items || []
    const requested = typeof route.query.source === 'string' ? route.query.source : ''
    const found = sources.value.find((source) => source.id === requested)
    activeId.value = found?.id || sources.value[0]?.id || ''
    if (activeId.value) {
      await load(activeSource.value, activePanel.value)
    }
  } catch (err) {
    sourcesError.value = err.message
  } finally {
    loadingSources.value = false
  }
}

onMounted(loadSources)

watch(
  () => route.query.source,
  (value) => {
    if (typeof value === 'string' && value && value !== activeId.value) {
      selectSource(value)
    }
  },
)

async function selectSource(id) {
  if (!sources.value.some((source) => source.id === id)) return
  activeId.value = id
  if (route.query.source !== id) {
    router.replace({ name: 'discover', query: { source: id } })
  }
  const panel = panelFor(activeSource.value)
  if (!panel.loaded && !panel.loading) {
    await load(activeSource.value, panel)
  }
}

async function load(source, panel) {
  if (!source || !panel || panel.loading) return
  panel.loading = true
  panel.error = ''
  try {
    const result =
      panel.mode === 'search' && panel.query.trim()
        ? await api.search(source.id, panel.query.trim(), panel.page)
        : await api.browse(source.id, panel.filters, panel.page)
    panel.items = result.items || []
    panel.hasMore = Boolean(result.hasMore)
  } catch (err) {
    panel.items = []
    panel.hasMore = false
    panel.error = err.message
  } finally {
    panel.loading = false
    panel.loaded = true
  }
}

async function submitSearch() {
  const panel = activePanel.value
  if (!panel) return
  panel.page = 1
  panel.mode = panel.query.trim() ? 'search' : 'browse'
  await load(activeSource.value, panel)
}

async function changeFilter() {
  const panel = activePanel.value
  if (!panel) return
  savedFilters.value[activeSource.value.id] = { ...panel.filters }
  persistFilters()
  panel.query = ''
  panel.mode = 'browse'
  panel.page = 1
  await load(activeSource.value, panel)
}

async function changePage(delta) {
  const panel = activePanel.value
  if (!panel) return
  const next = panel.page + delta
  if (next < 1) return
  panel.page = next
  await load(activeSource.value, panel)
}

function cover(item) {
  return api.imageUrl(item.cover, item.sourceId)
}
</script>

<template>
  <div class="discover-tabs-wrap">
    <div v-if="sources.length && !loadingSources" class="discover-head">
      <nav class="discover-tabs" aria-label="漫画源">
        <button
          v-for="source in sources"
          :key="source.id"
          type="button"
          :class="{ active: source.id === activeId }"
          @click="selectSource(source.id)"
        >
          {{ source.name }}
        </button>
      </nav>

      <div v-if="activeSource" class="source-search discover-search">
        <input
          v-model="activePanel.query"
          class="input"
          :disabled="!activeSource.canSearch || activePanel.loading"
          placeholder="搜索作品"
          @keyup.enter="submitSearch"
        />
        <button
          class="btn small"
          type="button"
          :disabled="!activeSource.canSearch || activePanel.loading"
          :aria-label="`搜索 ${activeSource.name}`"
          @click="submitSearch"
        >
          <Loader2 v-if="activePanel.loading" :size="15" class="spin" />
          <Search v-else :size="15" />
        </button>
      </div>
    </div>

    <div v-if="sourcesError" class="alert error">{{ sourcesError }}</div>
    <div v-else-if="loadingSources" class="source-loading">
      <Loader2 :size="22" class="spin" />
      <span>正在加载漫画源</span>
    </div>
    <div v-else-if="!sources.length" class="card empty">
      <ImageOff :size="26" />
      <span>所有漫画源均已在系统设置中隐藏</span>
    </div>

    <section v-else-if="activeSource && activePanel" class="source-page">
      <header class="source-page-head">
        <div>
          <h2>{{ activeSource.name }}</h2>
          <p>{{ activeSource.description }}</p>
        </div>
        <span class="badge primary">{{ activePanel.items.length }} 项</span>
      </header>

      <div v-if="activeSource.filters?.length" class="source-column-tools">
        <div class="source-filter-grid">
          <label v-for="group in activeSource.filters" :key="group.key" class="field compact">
            <span>{{ group.label }}</span>
            <select
              v-model="activePanel.filters[group.key]"
              class="select"
              :disabled="activePanel.loading"
              @change="changeFilter"
            >
              <option v-for="option in group.options" :key="`${group.key}-${option.value}`" :value="option.value">
                {{ option.label }}
              </option>
            </select>
          </label>
        </div>
      </div>

      <div v-if="activePanel.error" class="alert error">{{ activePanel.error }}</div>

      <div v-if="activePanel.loading && !activePanel.items.length" class="source-loading">
        <Loader2 :size="22" class="spin" />
        <span>正在加载</span>
      </div>

      <div v-else-if="!activePanel.items.length" class="source-empty">
        <ImageOff :size="24" />
        <span>没有作品</span>
      </div>

      <div v-else class="source-comic-grid">
        <RouterLink
          v-for="item in activePanel.items"
          :key="`${item.sourceId}-${item.id}`"
          class="comic-card source-comic-card"
          :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.id } }"
        >
          <div class="comic-cover">
            <img v-if="item.cover" :src="cover(item)" :alt="item.title" loading="lazy" />
            <span v-else class="cover-fallback"><BookOpen :size="26" /></span>
          </div>
          <div class="comic-body">
            <span class="comic-title">{{ item.title }}</span>
            <span class="comic-meta">{{ item.author || '未知作者' }}</span>
          </div>
        </RouterLink>
      </div>

      <div class="source-pagination">
        <button
          class="btn secondary small"
          type="button"
          :disabled="activePanel.page <= 1 || activePanel.loading"
          @click="changePage(-1)"
        >
          <ChevronLeft :size="15" />
          上一页
        </button>
        <span class="muted small">第 {{ activePanel.page }} 页</span>
        <button
          class="btn secondary small"
          type="button"
          :disabled="!activePanel.hasMore || activePanel.loading"
          @click="changePage(1)"
        >
          下一页
          <ChevronRight :size="15" />
        </button>
      </div>
    </section>
  </div>
</template>
