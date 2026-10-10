<script setup>
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  BookOpen,
  ChevronLeft,
  ChevronRight,
  Grid2X2,
  ImageOff,
  List,
  Loader2,
  Search,
} from 'lucide-vue-next'
import { api } from '../api'
import RoundedSelect from '../components/RoundedSelect.vue'
import { notify } from '../stores/notices'
import { readDiscoverNav, saveDiscoverNav } from '../stores/discover'

const FILTER_STORAGE_KEY = 'manco.discover.filters.v3'
const VIEW_STORAGE_KEY = 'manco.discover.view.v1'

const route = useRoute()
const router = useRouter()
const allSources = ref([])
const activeKind = ref('comic')
const activeId = ref('')
const jumpInput = ref('')
const panels = ref({})
const loadingSources = ref(true)
const sourcesError = ref('')
const savedFilters = ref(readSavedFilters())
const viewMode = ref(localStorage.getItem(VIEW_STORAGE_KEY) === 'list' ? 'list' : 'grid')

const kindCount = computed(() => (kind) => allSources.value.filter((source) => !source.hidden && kindOf(source) === kind).length)
const sources = computed(() => allSources.value.filter((source) => !source.hidden && kindOf(source) === activeKind.value))
const pageSize = computed(() => (viewMode.value === 'list' ? 30 : 49))

function kindOf(source) {
  if (source.kind === 'book' || source.kind === 'other') return source.kind
  return 'comic'
}

function readSavedFilters() {
  try {
    const value = JSON.parse(localStorage.getItem(FILTER_STORAGE_KEY) || '{}')
    return value && typeof value === 'object' ? value : {}
  } catch {
    return {}
  }
}

function persistFilters() {
  localStorage.setItem(FILTER_STORAGE_KEY, JSON.stringify(savedFilters.value))
}

function sourceFilters(source) {
  const stored = savedFilters.value?.[source.id] || {}
  const values = {}
  for (const group of source.filters || []) {
    const candidate = stored[group.key]
    const available = (group.options || []).some(
      (option) => String(option.value) === String(candidate) && !option.disabled,
    )
    values[group.key] = available
      ? String(candidate)
      : String(group.default ?? group.options?.find((option) => !option.disabled)?.value ?? '')
  }
  return values
}

function panelFor(source) {
  if (!panels.value[source.id]) {
    panels.value[source.id] = {
      query: '',
      mode: 'browse',
      page: 1,
      sourcePage: 1,
      sourceHasMore: true,
      items: [],
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
const visibleItems = computed(() => {
  const panel = activePanel.value
  if (!panel) return []
  const start = (panel.page - 1) * pageSize.value
  return panel.items.slice(start, start + pageSize.value)
})
const hasNextPage = computed(() => {
  const panel = activePanel.value
  if (!panel) return false
  return panel.sourceHasMore || panel.items.length > panel.page * pageSize.value
})

async function loadSources() {
  const nav = readDiscoverNav()
  try {
    const payload = await api.sources()
    allSources.value = payload.items || []
    const requestedKind = ['comic', 'book', 'other'].includes(route.query.kind) ? route.query.kind : ''
    activeKind.value = requestedKind || (['book', 'other'].includes(nav.kind) ? nav.kind : 'comic')
    const preferred = typeof route.query.source === 'string' ? route.query.source : nav.source || ''
    const found = sources.value.find((source) => source.id === preferred)
    activeId.value = found?.id || sources.value[0]?.id || ''
    if (activeId.value) {
      const panel = panelFor(activeSource.value)
      const restorePage = nav.source === activeId.value && nav.page ? nav.page : 1
      await ensure(activeSource.value, panel, restorePage)
      if (nav.source === activeId.value && nav.scrollTop) {
        nextTick(() => {
          const area = document.querySelector('.content-scroll')
          if (area) area.scrollTop = Number(nav.scrollTop) || 0
        })
      }
    }
  } catch (err) {
    sourcesError.value = err.message
  } finally {
    loadingSources.value = false
  }
}

onMounted(loadSources)

watch(
  () => [route.query.kind, route.query.source],
  ([kind, source]) => {
    if (typeof kind === 'string' && kind && kind !== activeKind.value && ['comic', 'book', 'other'].includes(kind)) {
      switchKind(kind)
      return
    }
    if (typeof source === 'string' && source && source !== activeId.value) selectSource(source)
  },
)

watch(viewMode, (value) => {
  localStorage.setItem(VIEW_STORAGE_KEY, value)
  const panel = activePanel.value
  if (panel) panel.page = 1
  afterLoad()
})

async function switchKind(kind) {
  if (!['comic', 'book', 'other'].includes(kind) || kind === activeKind.value) return
  activeKind.value = kind
  activeId.value = sources.value[0]?.id || ''
  if (route.query.kind !== kind || route.query.source !== activeId.value) {
    router.replace({ name: 'discover', query: { kind, source: activeId.value } })
  }
  if (activeId.value) await ensure(activeSource.value, panelFor(activeSource.value), 1)
  rememberPosition()
  scrollPageTop()
}

async function selectSource(id) {
  if (!sources.value.some((source) => source.id === id)) return
  activeId.value = id
  if (route.query.source !== id || route.query.kind !== activeKind.value) {
    router.replace({ name: 'discover', query: { kind: activeKind.value, source: id } })
  }
  const panel = panelFor(activeSource.value)
  if (!panel.loaded && !panel.loading) await ensure(activeSource.value, panel, 1)
  rememberPosition()
  scrollPageTop()
}

function fetchPage(source, panel, page) {
  return panel.mode === 'search' && panel.query.trim()
    ? api.search(source.id, panel.query.trim(), page)
    : api.browse(source.id, panel.filters, page)
}

async function ensure(source, panel, targetPage) {
  if (!source || !panel || panel.loading) return
  const wanted = Math.max(1, targetPage)
  const need = wanted * pageSize.value
  panel.loading = true
  panel.error = ''
  try {
    while (panel.items.length < need) {
      const result = await fetchPage(source, panel, panel.sourcePage)
      const items = result.items || []
      panel.items = panel.items.concat(items)
      panel.sourcePage += 1
      panel.sourceHasMore = Boolean(result.hasMore)
      if (!items.length) {
        panel.sourceHasMore = false
        break
      }
      if (!panel.sourceHasMore) break
    }
  } catch (err) {
    if (!panel.items.length) panel.error = err.message
    panel.sourceHasMore = false
    notify(`${source.name} 加载失败：${err.message}`, true)
  } finally {
    panel.loading = false
    panel.loaded = true
  }
  const maxPage = Math.max(1, Math.ceil(panel.items.length / pageSize.value))
  panel.page = panel.sourceHasMore ? wanted : Math.min(wanted, maxPage)
}

function resetPanel(panel) {
  panel.items = []
  panel.sourcePage = 1
  panel.sourceHasMore = true
  panel.page = 1
}

async function submitSearch() {
  const panel = activePanel.value
  if (!panel) return
  resetPanel(panel)
  panel.mode = panel.query.trim() ? 'search' : 'browse'
  await ensure(activeSource.value, panel, 1)
  afterLoad()
}

async function changeFilter() {
  const panel = activePanel.value
  if (!panel) return
  savedFilters.value[activeSource.value.id] = { ...panel.filters }
  persistFilters()
  panel.query = ''
  panel.mode = 'browse'
  resetPanel(panel)
  await ensure(activeSource.value, panel, 1)
  afterLoad()
}

async function goPage(page) {
  const panel = activePanel.value
  if (!panel || panel.loading) return
  const target = Math.max(1, Number(page) || 1)
  await ensure(activeSource.value, panel, target)
  afterLoad()
}

async function changePage(delta) {
  const panel = activePanel.value
  if (!panel) return
  const next = panel.page + delta
  if (next < 1) return
  if (delta > 0 && !hasNextPage.value) return
  await goPage(next)
}

function afterLoad() {
  jumpInput.value = ''
  rememberPosition()
  scrollPageTop()
}

function scrollPageTop() {
  nextTick(() => {
    const area = document.querySelector('.content-scroll')
    if (area) area.scrollTop = 0
  })
}

function jumpToPage() {
  const value = Number(jumpInput.value)
  if (!Number.isFinite(value) || value < 1) return
  goPage(Math.floor(value))
}

function rememberPosition() {
  saveDiscoverNav({
    kind: activeKind.value,
    source: activeId.value,
    page: activePanel.value?.page || 1,
    scrollTop: document.querySelector('.content-scroll')?.scrollTop || 0,
  })
}

function cover(item) {
  return api.imageUrl(item.cover, item.sourceId)
}

function filterOptions(group) {
  return (group.options || [])
    .filter((option) => !option.disabled)
    .map((option) => ({ value: String(option.value), label: option.label }))
}

function sourceKindLabel(kind) {
  return { comic: '漫画源', book: '书籍源', other: '其他源' }[kind] || '来源'
}
</script>

<template>
  <div class="discover-tabs-wrap">
    <div class="discover-head">
      <div class="inline discover-head-left">
        <div class="segmented" role="tablist" aria-label="资源类型">
          <button type="button" :class="{ active: activeKind === 'comic' }" @click="switchKind('comic')">
            漫画源 <span class="segmented-count">{{ kindCount('comic') }}</span>
          </button>
          <button type="button" :class="{ active: activeKind === 'book' }" @click="switchKind('book')">
            书籍源 <span class="segmented-count">{{ kindCount('book') }}</span>
          </button>
          <button type="button" :class="{ active: activeKind === 'other' }" @click="switchKind('other')">
            其他源 <span class="segmented-count">{{ kindCount('other') }}</span>
          </button>
        </div>
      </div>
      <div v-if="activeSource && activePanel" class="source-search discover-search">
        <Search :size="16" />
        <input
          v-model="activePanel.query"
          class="input"
          :disabled="!activeSource.canSearch || activePanel.loading"
          placeholder="搜索作品"
          @keyup.enter="submitSearch"
        />
        <button class="btn small" type="button" :disabled="!activeSource.canSearch || activePanel.loading" @click="submitSearch">
          <Loader2 v-if="activePanel.loading" :size="15" class="spin" />
          <Search v-else :size="15" />
        </button>
      </div>
    </div>

    <nav v-if="sources.length && !loadingSources" class="discover-source-strip" aria-label="来源">
      <button
        v-for="source in sources"
        :key="source.id"
        type="button"
        :class="{ active: source.id === activeId }"
        @click="selectSource(source.id)"
      >
        <img v-if="source.icon" class="source-tab-icon" :src="source.icon" alt="" />
        <span>{{ source.name }}</span>
      </button>
    </nav>

    <div v-if="sourcesError" class="alert error">{{ sourcesError }}</div>
    <div v-else-if="loadingSources" class="source-loading">
      <Loader2 :size="22" class="spin" />
      <span>正在加载资源</span>
    </div>
    <div v-else-if="!sources.length" class="card empty">
      <ImageOff :size="26" />
      <span>暂无可用的{{ sourceKindLabel(activeKind) }}，请先在资源仓库中启用来源。</span>
    </div>

    <section v-else-if="activeSource && activePanel" class="source-page">
      <header class="source-page-head">
        <div class="inline">
          <img v-if="activeSource.icon" class="source-favicon" :src="activeSource.icon" alt="" />
          <div>
            <h2>{{ activeSource.name }}</h2>
            <p>{{ activeSource.description }}</p>
          </div>
        </div>
        <span class="badge primary">{{ visibleItems.length }} / {{ activePanel.items.length }} 项</span>
      </header>

      <div class="source-column-tools">
        <div class="source-filter-grid">
          <label v-for="group in activeSource.filters || []" :key="group.key" class="field compact">
            <span>{{ group.label }}</span>
            <RoundedSelect
              v-model="activePanel.filters[group.key]"
              :label="group.label"
              :options="filterOptions(group)"
              @change="changeFilter"
            />
          </label>
          <div class="view-mode-control" role="group" aria-label="视图模式">
            <button
              class="icon-btn view-mode-button"
              type="button"
              :aria-label="viewMode === 'grid' ? '当前是网格视图，点击切换列表' : '当前是列表视图，点击切换网格'"
              :title="viewMode === 'grid' ? '当前：网格视图' : '当前：列表视图'"
              @click="viewMode = viewMode === 'grid' ? 'list' : 'grid'"
            >
              <component :is="viewMode === 'grid' ? Grid2X2 : List" :size="17" />
            </button>
          </div>
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
      <div v-else-if="viewMode === 'grid'" class="source-comic-grid source-comic-grid-7">
        <RouterLink
          v-for="item in visibleItems"
          :key="`${item.sourceId}-${item.id}`"
          class="comic-card source-comic-card"
          :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.id } }"
        >
          <div class="comic-cover">
            <img v-if="item.cover" :src="cover(item)" :alt="item.title" loading="lazy" decoding="async" />
            <span v-else class="cover-fallback"><BookOpen :size="26" /></span>
            <img v-if="activeSource.icon" class="comic-source-corner" :src="activeSource.icon" alt="" />
          </div>
          <div class="comic-body">
            <span class="comic-title">{{ item.title }}</span>
            <span class="comic-meta">{{ item.author || '未知作者' }}</span>
          </div>
        </RouterLink>
      </div>
      <div v-else class="source-list-view">
        <RouterLink
          v-for="item in visibleItems"
          :key="`${item.sourceId}-${item.id}`"
          class="source-list-card"
          :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.id } }"
        >
          <div class="source-list-cover">
            <img v-if="item.cover" :src="cover(item)" :alt="item.title" loading="lazy" />
            <span v-else class="cover-fallback"><BookOpen :size="24" /></span>
          </div>
          <div class="source-list-body">
            <strong>{{ item.title }}</strong>
            <span>{{ item.author || '未知作者' }} · {{ item.status || '连载中' }}</span>
            <p>{{ item.description || '暂无简介' }}</p>
          </div>
        </RouterLink>
      </div>

      <div class="source-pagination">
        <button class="btn secondary small" type="button" :disabled="activePanel.page <= 1 || activePanel.loading" @click="changePage(-1)">
          <ChevronLeft :size="15" /> 上一页
        </button>
        <span class="muted small">第 {{ activePanel.page }} 页 · 每页 {{ pageSize }} 项</span>
        <button class="btn secondary small" type="button" :disabled="!hasNextPage || activePanel.loading" @click="changePage(1)">
          下一页 <ChevronRight :size="15" />
        </button>
        <form class="page-jump" @submit.prevent="jumpToPage">
          <span class="muted small">跳至</span>
          <input v-model="jumpInput" class="input page-jump-input" type="number" min="1" placeholder="#" />
          <button class="btn secondary small" type="submit" :disabled="activePanel.loading">跳转</button>
        </form>
      </div>
    </section>
  </div>
</template>
