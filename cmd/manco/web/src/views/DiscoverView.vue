<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  BookOpen,
  BookMarked,
  ChevronLeft,
  ChevronRight,
  Film,
  Grid2X2,
  ImageOff,
  List,
  Loader2,
  Search,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import ContentLink from '../components/ContentLink.vue'
import RoundedSelect from '../components/RoundedSelect.vue'
import SourceIcon from '../components/SourceIcon.vue'
import { notify } from '../stores/notices'
import { rememberComicPreview } from '../stores/comicPreview'
import {
  discoverPanelsExpired,
  markDiscoverPanelsLoaded,
  readDiscoverNav,
  readDiscoverPanels,
  resetDiscoverPanels,
  saveDiscoverNav,
} from '../stores/discover'

const FILTER_STORAGE_KEY = 'manco.discover.filters.v3'
const VIEW_STORAGE_KEY = 'manco.discover.view.v1'

const route = useRoute()
const router = useRouter()
const allSources = ref([])
const activeKind = ref('comic')
const activeId = ref('')
const jumpInput = ref('')
const panels = ref(readDiscoverPanels())
const loadingSources = ref(true)
const sourcesError = ref('')
const savedFilters = ref(readSavedFilters())
const viewMode = ref(localStorage.getItem(VIEW_STORAGE_KEY) === 'list' ? 'list' : 'grid')
const sourceStripRef = ref(null)
const gridRef = ref(null)
const stripCanLeft = ref(false)
const stripCanRight = ref(false)
const gridColumns = ref(7)
const requestStates = new Map()

const kindCount = computed(() => (kind) => allSources.value.filter((source) => !source.hidden && kindOf(source) === kind).length)
const sources = computed(() => allSources.value.filter((source) => !source.hidden && kindOf(source) === activeKind.value))
const pageSize = computed(() => (viewMode.value === 'list' ? 30 : Math.max(1, gridColumns.value * 7)))

// Kototoro 的排序能力会作为 sort 筛选组返回；没有声明的来源统一提供“最新”，
// 并把排序器固定放在网格/列表按钮左侧。
const DEFAULT_SORT_GROUP = {
  key: 'sort',
  label: '排序',
  default: '',
  options: [{ value: '', label: '最新' }],
}

const activeFilters = computed(() =>
  (activeSource.value?.filters || []).filter((group) => group.key !== 'sort' && group.key !== 'order'),
)

const activeSortGroup = computed(() => {
  const groups = activeSource.value?.filters || []
  return groups.find((group) => group.key === 'sort') || groups.find((group) => group.key === 'order') || DEFAULT_SORT_GROUP
})


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

function sortOptions(options) {
  const list = (options || []).map((option) => ({ value: String(option.value), label: option.label }))
  if (!list.length) return [{ value: '', label: '最新' }]
  return list.some((option) => option.value === '') ? list : [{ value: '', label: '最新' }, ...list]
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
  if (!('sort' in values)) values.sort = String(stored.sort || '')
  return values
}

async function ensureSourceFilters(source) {
  if (!source || source.filtersLoaded || (source.filters || []).length) return
  source.filtersLoading = true
  try {
    const result = await api.sourceFilters(source.id)
    source.filters = Array.isArray(result.items) ? result.items : []
  } catch (err) {
    source.filters = []
    notify(`${source.name} 筛选加载失败：${err.message}`, true)
  } finally {
    source.filtersLoaded = true
    source.filtersLoading = false
    const panel = panels.value[source.id]
    if (panel) panel.filters = sourceFilters(source)
  }
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
      searching: false,
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

onUnmounted(() => {
  window.removeEventListener('resize', updateStripBounds)
  for (const state of requestStates.values()) state.controller.abort()
  requestStates.clear()
})

async function loadSources() {
  const nav = readDiscoverNav()
  if (discoverPanelsExpired()) resetDiscoverPanels()
  try {
    const payload = await api.sources()
    allSources.value = payload.items || []
    const requestedKind = ['comic', 'book', 'other'].includes(route.query.kind) ? route.query.kind : ''
    activeKind.value = requestedKind || (['book', 'other'].includes(nav.kind) ? nav.kind : 'comic')
    const preferred = typeof route.query.source === 'string' ? route.query.source : nav.source || ''
    const found = sources.value.find((source) => source.id === preferred)
    activeId.value = found?.id || sources.value[0]?.id || ''
    loadingSources.value = false
    if (activeId.value) {
      const panel = panelFor(activeSource.value)
      const restorePage = nav.source === activeId.value && nav.page ? nav.page : panel.page || 1
      void ensure(activeSource.value, panel, restorePage).then(() => {
        if (nav.source === activeId.value && nav.scrollTop) {
          nextTick(() => {
            const area = document.querySelector('.content-scroll')
            if (area) area.scrollTop = Number(nav.scrollTop) || 0
          })
        }
      })
    } else {
      markDiscoverPanelsLoaded()
    }
  } catch (err) {
    sourcesError.value = err.message
  } finally {
    loadingSources.value = false
  }
}

onMounted(() => {
  window.addEventListener('resize', updateStripBounds)
  loadSources().then(() => {
    nextTick(updateStripBounds)
    nextTick(measureGridColumns)
  })
})

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

function updateStripBounds() {
  const element = sourceStripRef.value
  if (!element) {
    stripCanLeft.value = false
    stripCanRight.value = false
    return
  }
  stripCanLeft.value = element.scrollLeft > 2
  stripCanRight.value = element.scrollLeft + element.clientWidth < element.scrollWidth - 2
  nextTick(measureGridColumns)
}

function measureGridColumns() {
  nextTick(() => {
    const element = gridRef.value
    if (!element) return
    const tracks = window.getComputedStyle(element).gridTemplateColumns.split(/\s+/).filter(Boolean)
    if (tracks.length) gridColumns.value = tracks.length
  })
}

function scrollStrip(direction) {
  const element = sourceStripRef.value
  if (!element) return
  element.scrollBy({ left: direction * Math.max(160, element.clientWidth * 0.7), behavior: 'smooth' })
}

function handleStripWheel(event) {
  const element = sourceStripRef.value
  if (!element) return
  // 悬停在这一行时滚轮只用于横向滚动，到顶/到底也不再带动整页上下滚。
  event.preventDefault()
  if (element.scrollWidth <= element.clientWidth) return
  const delta = Math.abs(event.deltaY) >= Math.abs(event.deltaX) ? event.deltaY : event.deltaX
  if (delta === 0) return
  element.scrollBy({ left: delta, behavior: 'auto' })
}

watch(sources, () => {
  nextTick(updateStripBounds)
})
watch(viewMode, (value) => {
  localStorage.setItem(VIEW_STORAGE_KEY, value)
  const panel = activePanel.value
  if (panel) panel.page = 1
  nextTick(measureGridColumns)
  afterLoad()
})

watch(pageSize, () => {
  const panel = activePanel.value
  if (!panel?.loaded || panel.loading) return
  void ensure(activeSource.value, panel, panel.page)
})

async function switchKind(kind) {
  if (!['comic', 'book', 'other'].includes(kind) || kind === activeKind.value) return
  activeKind.value = kind
  activeId.value = sources.value[0]?.id || ''
  if (route.query.kind !== kind || route.query.source !== activeId.value) {
    router.replace({ name: 'discover', query: { kind, source: activeId.value } })
  }
  rememberPosition()
  scrollPageTop()
  if (activeId.value) void ensure(activeSource.value, panelFor(activeSource.value), 1)
}

async function selectSource(id) {
  if (!sources.value.some((source) => source.id === id)) return
  activeId.value = id
  if (route.query.source !== id || route.query.kind !== activeKind.value) {
    router.replace({ name: 'discover', query: { kind: activeKind.value, source: id } })
  }
  const panel = panelFor(activeSource.value)
  rememberPosition()
  scrollPageTop()
  if (!panel.loaded && !panel.loading) void ensure(activeSource.value, panel, 1)
}

function beginPanelRequest(source, panel) {
  const previous = requestStates.get(source.id)
  previous?.controller.abort()
  const state = {
    seq: (previous?.seq || 0) + 1,
    controller: new AbortController(),
  }
  requestStates.set(source.id, state)
  panel.loading = true
  panel.searching = panel.mode === 'search' && Boolean(panel.query.trim())
  return state
}

function isCurrentPanelRequest(source, state) {
  return requestStates.get(source.id)?.seq === state.seq
}

function finishPanelRequest(source, panel, state) {
  if (!isCurrentPanelRequest(source, state)) return false
  requestStates.delete(source.id)
  panel.loading = false
  panel.searching = false
  return true
}

function fetchPage(source, panel, page, signal) {
  return panel.mode === 'search' && panel.query.trim()
    ? api.search(source.id, panel.query.trim(), page, signal)
    : api.browse(source.id, panel.filters, page, signal)
}

async function ensure(source, panel, targetPage) {
  if (!source || !panel) return
  const wanted = Math.max(1, targetPage)
  const need = wanted * pageSize.value
  const request = beginPanelRequest(source, panel)
  panel.error = ''
  let completed = false
  try {
    await ensureSourceFilters(source)
    if (!isCurrentPanelRequest(source, request)) return
    while (panel.items.length < need) {
      const result = await fetchPage(source, panel, panel.sourcePage, request.controller.signal)
      if (!isCurrentPanelRequest(source, request)) return
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
    if (!isCurrentPanelRequest(source, request) || err?.name === 'AbortError') return
    if (!panel.items.length) panel.error = err.message
    panel.sourceHasMore = false
    notify(`${source.name} 加载失败：${err.message}`, true)
  } finally {
    completed = finishPanelRequest(source, panel, request)
  }
  if (!completed) return
  panel.loaded = true
  markDiscoverPanelsLoaded()
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

async function clearSourceSearch() {
  const panel = activePanel.value
  if (!panel) return
  panel.query = ''
  await submitSearch()
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
  nextTick(measureGridColumns)
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
  if (item.cachedCover) return item.cachedCover
    return api.imageUrl(item.cover, item.sourceId, '', 320)
}

function filterOptions(group) {
  return (group.options || [])
    .filter((option) => !option.disabled)
    .map((option) => ({ value: String(option.value), label: option.label }))
}

function sourceKindLabel(kind) {
  return { comic: '漫画源', book: '书籍源', other: '其他源' }[kind] || '来源'
}

// 进入详情前把列表卡片数据留下来，详情页可以立刻渲染首屏。
function openComic(item) {
  rememberComicPreview(item)
  rememberPosition()
}

function latestLabel(item) {
  const latest = String(item.latestChapterTitle || '').trim()
  if (/完结|完結|已完|completed|finished/i.test(String(item.status || ''))) {
    return `完结至 ${latest || '最后章节'}`
  }
  return `更新至 ${latest || '最新章节'}`
}
</script>

<template>
  <div class="discover-tabs-wrap">
    <div class="discover-head">
      <div class="inline discover-head-left">
        <div class="segmented" role="tablist" aria-label="资源类型">
          <button type="button" :class="{ active: activeKind === 'comic' }" @click="switchKind('comic')">
            <BookOpen :size="18" />
            漫画源 <span class="segmented-count">{{ kindCount('comic') }}</span>
          </button>
          <button type="button" :class="{ active: activeKind === 'book' }" @click="switchKind('book')">
            <BookMarked :size="18" />
            书籍源 <span class="segmented-count">{{ kindCount('book') }}</span>
          </button>
          <button type="button" :class="{ active: activeKind === 'other' }" @click="switchKind('other')">
            <Film :size="18" />
            其他源 <span class="segmented-count">{{ kindCount('other') }}</span>
          </button>
        </div>
      </div>
      <div v-if="activeSource && activePanel" class="discover-search">
        <label class="discover-search-field">
          <Search :size="15" />
          <input
            v-model="activePanel.query"
            class="discover-search-input"
            type="search"
            :disabled="!activeSource.canSearch"
            placeholder="搜索作品"
            @keyup.enter="submitSearch"
          />
          <Loader2 v-if="activePanel.searching" :size="15" class="spin" />
          <button v-else-if="activePanel.query" class="icon-btn" type="button" aria-label="清除搜索" @click="clearSourceSearch">
            <X :size="14" />
          </button>
        </label>
      </div>
      <div v-if="activeSource && activePanel" class="view-mode-control" role="group" aria-label="视图模式">
        <button
          class="view-mode-button"
          type="button"
          :aria-label="viewMode === 'grid' ? '当前是网格视图，点击切换列表' : '当前是列表视图，点击切换网格'"
          :title="viewMode === 'grid' ? '切换为列表视图' : '切换为网格视图'"
          @click="viewMode = viewMode === 'grid' ? 'list' : 'grid'"
        >
          <component :is="viewMode === 'grid' ? Grid2X2 : List" :size="19" />
        </button>
      </div>
    </div>

    <div v-if="sources.length && !loadingSources" class="discover-source-row">
      <button
        class="strip-arrow"
        type="button"
        :class="{ disabled: !stripCanLeft }"
        aria-label="向左滚动来源"
        @click="scrollStrip(-1)"
      >
        <ChevronLeft :size="16" />
      </button>
      <nav
        ref="sourceStripRef"
        class="discover-source-strip"
        aria-label="来源"
        @scroll="updateStripBounds"
        @wheel="handleStripWheel"
      >
        <button
          v-for="source in sources"
          :key="source.id"
          type="button"
          :data-name="source.name"
          :title="source.name"
          :class="{ active: source.id === activeId }"
          @click="selectSource(source.id)"
        >
          <SourceIcon class="source-tab-icon" :src="source.icon" :kind="kindOf(source)" :alt="source.name" :size="18" />
          <span>{{ source.name }}</span>
        </button>
      </nav>
      <button
        class="strip-arrow"
        type="button"
        :class="{ disabled: !stripCanRight }"
        aria-label="向右滚动来源"
        @click="scrollStrip(1)"
      >
        <ChevronRight :size="16" />
      </button>
    </div>

    <div v-if="sourcesError" class="alert error">{{ sourcesError }}</div>
    <div v-else-if="loadingSources" class="source-loading">
      <Loader2 :size="22" class="spin" />
      <span>正在加载资源</span>
    </div>
    <div v-else-if="!sources.length" class="card empty">
      <ImageOff :size="26" />
      <span>暂无可用的{{ sourceKindLabel(activeKind) }}，请先在资源拓展中启用来源。</span>
    </div>

    <section v-else-if="activeSource && activePanel" class="source-page">

      <div class="source-column-tools">
        <div class="source-filter-grid">
          <label v-for="group in activeFilters" :key="group.key" class="field compact filter-chip">
            <RoundedSelect
              v-model="activePanel.filters[group.key]"
              :label="group.label"
              :caption="group.label"
              :options="filterOptions(group)"
              @change="changeFilter"
            />
          </label>
          <span class="spacer" />
          <div class="filter-chip sort-chip">
            <RoundedSelect
              v-model="activePanel.filters[activeSortGroup.key]"
              :label="activeSortGroup.label"
              :caption="activeSortGroup.label"
              :options="sortOptions(activeSortGroup.options)"
              @change="changeFilter"
            />
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
      <div v-else-if="viewMode === 'grid'" ref="gridRef" class="source-comic-grid source-comic-grid-7">
        <ContentLink
          v-for="item in visibleItems"
          :key="`${item.sourceId}-${item.id}`"
          class="comic-card source-comic-card"
          :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.id } }"
          @click="openComic(item)"
        >
          <div class="comic-cover">
            <img v-if="item.cover || item.cachedCover" :src="cover(item)" :alt="item.title" loading="lazy" decoding="async" />
            <span v-else class="cover-fallback"><BookOpen :size="26" /></span>
          </div>
          <div class="comic-body">
            <span class="comic-title">{{ item.title }}</span>
            <span class="comic-meta">{{ latestLabel(item) }}</span>
          </div>
        </ContentLink>
      </div>
      <div v-else class="source-list-view">
        <ContentLink
          v-for="item in visibleItems"
          :key="`${item.sourceId}-${item.id}`"
          class="source-list-card"
          :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.id } }"
          @click="openComic(item)"
        >
          <div class="source-list-cover">
            <img v-if="item.cover || item.cachedCover" :src="cover(item)" :alt="item.title" loading="lazy" decoding="async" />
            <span v-else class="cover-fallback"><BookOpen :size="24" /></span>
          </div>
          <div class="source-list-body">
            <strong>{{ item.title }}</strong>
            <span>{{ item.author || '未知作者' }} · {{ item.status || '连载中' }}</span>
            <p>{{ item.description || '暂无简介' }}</p>
          </div>
        </ContentLink>
      </div>

      <div class="source-pagination">
        <button class="btn secondary small" type="button" :disabled="activePanel.page <= 1 || activePanel.loading" @click="changePage(-1)">
          <ChevronLeft :size="15" /> 上一页
        </button>
        <span class="muted small">第 {{ activePanel.page }} 页</span>
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
