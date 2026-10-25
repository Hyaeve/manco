<script setup>
import { computed, nextTick, onActivated, onDeactivated, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  BookOpen,
  BookMarked,
  ChevronLeft,
  ChevronRight,
  EyeOff,
  Film,
  Grid2X2,
  ImageOff,
  List,
  Loader2,
  RefreshCw,
  Rss,
  Search,
  Sparkles,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import BangumiFeedIcon from '../components/BangumiFeedIcon.vue'
import ContentLink from '../components/ContentLink.vue'
import ComicSourceIcon from '../components/ComicSourceIcon.vue'
import RoundedSelect from '../components/RoundedSelect.vue'
import SourceIcon from '../components/SourceIcon.vue'
import { askConfirm } from '../stores/confirm'
import { lastSubscriptionDownloadDir, rememberSubscriptionDownloadDir } from '../stores/subscriptionPreferences'
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
import { SOURCES_CHANGED_EVENT } from '../utils/sourceEvents'

defineOptions({ name: 'DiscoverView' })

const FILTER_STORAGE_KEY = 'manco.discover.filters.v3'
const VIEW_STORAGE_KEY = 'manco.discover.view.v1'
const BANGUMI_FEEDS = [
  { id: 'bangumi-recent', feed: 'recent', name: '近期注目', kind: 'bangumi', homepage: 'https://bgm.tv/', canSearch: true, filters: [], filtersLoaded: true },
  { id: 'bangumi-airing', feed: 'airing', name: '连载中', kind: 'bangumi', homepage: 'https://bgm.tv/', canSearch: true, filters: [], filtersLoaded: true },
  { id: 'bangumi-monthly', feed: 'monthly', name: '本月新作', kind: 'bangumi', homepage: 'https://bgm.tv/', canSearch: true, filters: [], filtersLoaded: true },
  { id: 'bangumi-yearly', feed: 'yearly', name: '今年新作', kind: 'bangumi', homepage: 'https://bgm.tv/', canSearch: true, filters: [], filtersLoaded: true },
  { id: 'bangumi-popular', feed: 'popular', name: '收藏最多', kind: 'bangumi', homepage: 'https://bgm.tv/', canSearch: true, filters: [], filtersLoaded: true },
  { id: 'bangumi-ranking', feed: 'ranking', name: '排行榜', kind: 'bangumi', homepage: 'https://bgm.tv/', canSearch: true, filters: [], filtersLoaded: true },
  { id: 'bangumi-manga', feed: 'manga', name: '高分漫画', kind: 'bangumi', homepage: 'https://bgm.tv/', canSearch: true, filters: [], filtersLoaded: true },
  { id: 'bangumi-novel', feed: 'novel', name: '高分小说', kind: 'bangumi', homepage: 'https://bgm.tv/', canSearch: true, filters: [], filtersLoaded: true },
]

const route = useRoute()
const router = useRouter()
const allSources = ref([])
const sourceAccounts = ref({})
const selectedItems = ref(new Set())
const batchCoverBlur = ref(false)
const batchSubscribing = ref(false)
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
const gridColumns = ref(responsiveGridColumns())
const refreshRevealing = ref(false)
const requestStates = new Map()
let refreshRevealTimer = 0
let stripPersistTimer = 0
let selectionAnchor = -1
let removeDiscoverRouteRestore = null

const kindCount = computed(() => (kind) => kind === 'bangumi'
  ? BANGUMI_FEEDS.length
  : allSources.value.filter((source) => !source.hidden && kindOf(source) === kind).length)
const sources = computed(() => activeKind.value === 'bangumi'
  ? BANGUMI_FEEDS
  : allSources.value.filter((source) => !source.hidden && kindOf(source) === activeKind.value))
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
  if (source.kind === 'book' || source.kind === 'other' || source.kind === 'bangumi') return source.kind
  return 'comic'
}

function responsiveGridColumns(width = window.innerWidth) {
  if (width <= 460) return 2
  if (width <= 650) return 3
  if (width <= 920) return 4
  if (width <= 1180) return 5
  if (width <= 1500) return 6
  return 7
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

function applyServerBrowseFilters(raw) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return
  const merged = { ...savedFilters.value }
  for (const [sourceId, values] of Object.entries(raw)) {
    if (!values || typeof values !== 'object' || Array.isArray(values)) continue
    const normalized = {}
    for (const [key, value] of Object.entries(values)) {
      const cleanKey = String(key || '').trim()
      const cleanValue = String(value ?? '').trim()
      if (cleanKey && cleanValue) normalized[cleanKey] = cleanValue
    }
    if (Object.keys(normalized).length) merged[sourceId] = normalized
  }
  savedFilters.value = merged
  persistFilters()
  for (const source of allSources.value) {
    if (!(source.filters || []).length) continue
    const panel = panels.value[source.id]
    if (panel) panel.filters = sourceFilters(source)
  }
}

function persistSourceBrowseFilters(source, filters) {
  if (!source?.id || source.kind === 'bangumi') return
  void api.updateSource(source.id, { browseFilters: { ...filters } }).catch(() => {})
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
    if (panel) {
      panel.filters = sourceFilters(source)
      persistSourceBrowseFilters(source, panel.filters)
    }
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
const loadingPlaceholderCount = computed(() => {
  if (!activePanel.value?.loading) return 0
  return Math.max(0, pageSize.value - visibleItems.value.length)
})
const discoverSelecting = computed(() => selectedItems.value.size > 0)
const activeSourceDefaultDir = computed(() => {
  const account = sourceAccounts.value[activeSource.value?.id]
  return String(account?.settings?.defaultDownloadDir || '').trim()
})
const activeSourceCoverBlur = computed(() => Boolean(sourceAccounts.value[activeSource.value?.id]?.settings?.coverBlur))
const canBatchSubscribe = computed(() =>
  activeKind.value === 'bangumi' || Boolean(activeSourceDefaultDir.value),
)
const hasNextPage = computed(() => {
  const panel = activePanel.value
  if (!panel) return false
  return panel.sourceHasMore || panel.items.length > panel.page * pageSize.value
})

onUnmounted(() => {
  window.removeEventListener('resize', updateStripBounds)
  window.removeEventListener('keydown', handleDiscoverKeydown)
  window.removeEventListener(SOURCES_CHANGED_EVENT, handleSourcesChanged)
  window.clearTimeout(refreshRevealTimer)
  window.clearTimeout(stripPersistTimer)
  removeDiscoverRouteRestore?.()
  removeDiscoverRouteRestore = null
  for (const state of requestStates.values()) state.controller.abort()
  requestStates.clear()
})

async function loadSources() {
  const nav = readDiscoverNav()
  if (discoverPanelsExpired()) resetDiscoverPanels()
  try {
    const payload = await api.sources()
    allSources.value = payload.items || []
    sourceAccounts.value = payload.accounts || {}
    applyServerBrowseFilters(payload.browseFilters)
    const requestedKind = ['bangumi', 'comic', 'book', 'other'].includes(route.query.kind) ? route.query.kind : ''
    activeKind.value = requestedKind || (['bangumi', 'book', 'other'].includes(nav.kind) ? nav.kind : 'comic')
    const preferred = typeof route.query.source === 'string' ? route.query.source : nav.source || ''
    const found = sources.value.find((source) => source.id === preferred)
    activeId.value = found?.id || sources.value[0]?.id || ''
    loadingSources.value = false
    if (activeId.value) {
      const panel = panelFor(activeSource.value)
      persistSourceBrowseFilters(activeSource.value, panel.filters)
      const restorePage = nav.source === activeId.value && nav.page ? nav.page : panel.page || 1
      restoreSourceStripScroll(nav)
      nextTick(rememberPosition)
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
  window.addEventListener('keydown', handleDiscoverKeydown)
  window.addEventListener(SOURCES_CHANGED_EVENT, handleSourcesChanged)
  removeDiscoverRouteRestore = router.afterEach((to, from) => {
    if (to.name === 'discover' && from.name !== 'discover') {
      restoreSourceStripScroll(readDiscoverNav())
    }
  })
  loadSources().then(() => {
    nextTick(updateStripBounds)
    nextTick(measureGridColumns)
  })
})

onActivated(() => {
  restoreSourceStripScroll(readDiscoverNav())
})

onDeactivated(() => {
  rememberPosition()
})

function handleSourcesChanged() {
  void loadSources().then(() => {
    nextTick(updateStripBounds)
    nextTick(measureGridColumns)
  })
}

function handleDiscoverKeydown(event) {
  if (event.key === 'Escape' && discoverSelecting.value) clearDiscoverSelection()
}

watch(
  () => [route.query.kind, route.query.source],
  ([kind, source]) => {
    if (typeof kind === 'string' && kind && kind !== activeKind.value && ['bangumi', 'comic', 'book', 'other'].includes(kind)) {
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

function handleSourceStripScroll() {
  updateStripBounds()
  window.clearTimeout(stripPersistTimer)
  stripPersistTimer = window.setTimeout(rememberPosition, 140)
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
  if (!['bangumi', 'comic', 'book', 'other'].includes(kind) || kind === activeKind.value) return
  rememberPosition()
  clearDiscoverSelection()
  const nav = readDiscoverNav()
  activeKind.value = kind
  const rememberedSource = String(nav.sourceByKind?.[kind] || '').trim()
  const remembered = sources.value.find((source) => source.id === rememberedSource)
  activeId.value = remembered?.id || sources.value[0]?.id || ''
  if (route.query.kind !== kind || route.query.source !== activeId.value) {
    router.replace({ name: 'discover', query: { kind, source: activeId.value } })
  }
  rememberPosition()
  restoreSourceStripScroll({
    source: activeId.value,
    sourceScrollLeft: nav.sourceScrollLeftByKind?.[kind] || 0,
  })
  scrollPageTop()
  if (activeId.value) void ensure(activeSource.value, panelFor(activeSource.value), 1)
}

async function selectSource(id) {
  if (!sources.value.some((source) => source.id === id)) return
  clearDiscoverSelection()
  activeId.value = id
  if (route.query.source !== id || route.query.kind !== activeKind.value) {
    router.replace({ name: 'discover', query: { kind: activeKind.value, source: id } })
  }
  const panel = panelFor(activeSource.value)
  persistSourceBrowseFilters(activeSource.value, panel.filters)
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
  if (source.kind === 'bangumi') {
    if (panel.mode === 'search' && panel.query.trim()) {
      return api.bangumiSearch(panel.query.trim(), page, pageSize.value, signal)
    }
    return api.bangumiBrowse(source.feed, page, pageSize.value, signal)
  }
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
    let sourcePagesFetched = 0
    while (panel.items.length < need && sourcePagesFetched < 12) {
      const result = await fetchPage(source, panel, panel.sourcePage, request.controller.signal)
      if (!isCurrentPanelRequest(source, request)) return
      const items = result.items || []
      const seen = new Set(panel.items.map((item) => `${item.sourceId}:${item.id}`))
      const newItems = items.filter((item) => {
        const key = `${item.sourceId}:${item.id}`
        if (seen.has(key)) return false
        seen.add(key)
        return true
      })
      panel.items = panel.items.concat(newItems)
      panel.sourcePage += 1
      panel.sourceHasMore = Boolean(result.hasMore)
      sourcePagesFetched += 1
      if (!items.length) {
        panel.sourceHasMore = false
        break
      }
      if (!newItems.length) break
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
  clearDiscoverSelection()
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
  clearDiscoverSelection()
  savedFilters.value[activeSource.value.id] = { ...panel.filters }
  persistFilters()
  persistSourceBrowseFilters(activeSource.value, panel.filters)
  panel.query = ''
  panel.mode = 'browse'
  resetPanel(panel)
  await ensure(activeSource.value, panel, 1)
  afterLoad()
}

async function refreshCurrentPanel() {
  const source = activeSource.value
  const panel = activePanel.value
  if (!source || !panel || panel.loading) return
  clearDiscoverSelection()
  refreshRevealing.value = false
  resetPanel(panel)
  await ensure(source, panel, 1)
  if (panel.items.length) {
    refreshRevealing.value = true
    window.clearTimeout(refreshRevealTimer)
    refreshRevealTimer = window.setTimeout(() => {
      refreshRevealing.value = false
    }, 620)
  }
  afterLoad()
}

async function goPage(page) {
  const panel = activePanel.value
  if (!panel || panel.loading) return
  clearDiscoverSelection()
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
  const nav = readDiscoverNav()
  const sourceScrollLeft = sourceStripRef.value?.scrollLeft || 0
  saveDiscoverNav({
    kind: activeKind.value,
    source: activeId.value,
    page: activePanel.value?.page || 1,
    scrollTop: document.querySelector('.content-scroll')?.scrollTop || 0,
    sourceScrollLeft,
    sourceByKind: {
      ...(nav.sourceByKind || {}),
      [activeKind.value]: activeId.value,
    },
    sourceScrollLeftByKind: {
      ...(nav.sourceScrollLeftByKind || {}),
      [activeKind.value]: sourceScrollLeft,
    },
  })
}

function restoreSourceStripScroll(nav) {
  const savedTarget = nav.source === activeId.value ? Number(nav.sourceScrollLeft) || 0 : 0
  const apply = () => {
    const strip = sourceStripRef.value
    if (!strip) return
    const activeButton = strip.querySelector('button.active')
    const centeredTarget = activeButton
      ? Math.max(0, activeButton.offsetLeft - (strip.clientWidth - activeButton.offsetWidth) / 2)
      : 0
    const target = savedTarget || centeredTarget
    strip.scrollLeft = target
    updateStripBounds()
  }
  nextTick(apply)
  window.requestAnimationFrame(() => window.requestAnimationFrame(apply))
  window.setTimeout(apply, 160)
}

function cover(item) {
  if (item.cachedCover) return item.cachedCover
  const source = allSources.value.find((entry) => entry.id === item.sourceId)
  if (String(item.sourceId || '').startsWith('bangumi-')) {
    return api.imageUrl(item.cover, item.sourceId, source?.homepage || 'https://bgm.tv/', 320)
  }
  return api.imageUrl(item.cover, item.sourceId, source?.homepage || '', 320)
}

function filterOptions(group) {
  return (group.options || [])
    .filter((option) => !option.disabled)
    .map((option) => ({ value: String(option.value), label: option.label }))
}

function sourceKindLabel(kind) {
  return { bangumi: 'Bangumi', comic: '漫画源', book: '书籍源', other: '视频源' }[kind] || '来源'
}

function discoveryKey(item) {
  return `${item.sourceId}:${item.id}`
}

function isDiscoverSelected(item) {
  return selectedItems.value.has(discoveryKey(item))
}

function clearDiscoverSelection() {
  selectedItems.value = new Set()
  selectionAnchor = -1
  batchCoverBlur.value = false
}

function enterDiscoverSelection(event, item, index) {
  event.preventDefault()
  if (!selectedItems.value.size) {
    batchCoverBlur.value = activeSourceCoverBlur.value
  }
  const next = new Set(selectedItems.value)
  if (event.shiftKey && selectionAnchor >= 0) {
    const [start, end] = selectionAnchor < index ? [selectionAnchor, index] : [index, selectionAnchor]
    for (let cursor = start; cursor <= end; cursor += 1) {
      const row = visibleItems.value[cursor]
      if (row) next.add(discoveryKey(row))
    }
  } else {
    next.add(discoveryKey(item))
  }
  selectedItems.value = next
  if (!next.size) batchCoverBlur.value = false
  selectionAnchor = index
}

function handleDiscoverItemClick(event, item, index) {
  if (!discoverSelecting.value) return
  if (event?.button !== 0) return
  const next = new Set(selectedItems.value)
  if (event.shiftKey && selectionAnchor >= 0) {
    const [start, end] = selectionAnchor < index ? [selectionAnchor, index] : [index, selectionAnchor]
    for (let cursor = start; cursor <= end; cursor += 1) {
      const row = visibleItems.value[cursor]
      if (row) next.add(discoveryKey(row))
    }
  } else if (next.has(discoveryKey(item))) {
    next.delete(discoveryKey(item))
  } else {
    next.add(discoveryKey(item))
  }
  selectedItems.value = next
  if (!next.size) batchCoverBlur.value = false
  selectionAnchor = index
}

function handleDiscoverCardClick(event, item, index) {
  if (discoverSelecting.value) {
    handleDiscoverItemClick(event, item, index)
    return
  }
  if (isBangumiItem(item)) return
  openComic(item)
}

function isBangumiItem(item) {
  return String(item?.sourceId || '').startsWith('bangumi-')
}

function beforeDiscoverNavigate(item) {
  if (discoverSelecting.value) return false
  if (!isBangumiItem(item)) return true
  const target = String(item.sourceUrl || '').trim()
  if (target) window.open(target, '_blank', 'noopener,noreferrer')
  return false
}

function toggleAllDiscoverItems() {
  if (selectedItems.value.size < visibleItems.value.length) {
    selectedItems.value = new Set(visibleItems.value.map(discoveryKey))
  } else {
    clearDiscoverSelection()
  }
}

function defaultSubscriptionCron() {
  const now = new Date()
  return `${now.getMinutes()} ${now.getHours()} * * ${now.getDay()}`
}

async function batchSubscribeSelected() {
  const rows = visibleItems.value.filter((item) => selectedItems.value.has(discoveryKey(item)))
  const downloadDir = activeKind.value === 'bangumi'
    ? activeSourceDefaultDir.value || lastSubscriptionDownloadDir()
    : activeSourceDefaultDir.value
  if (!rows.length || batchSubscribing.value) return
  if (activeKind.value !== 'bangumi' && !downloadDir) {
    notify('当前内容源未设置默认下载目录，无法批量订阅', 'warning')
    return
  }
  const confirmed = await askConfirm({
    title: '批量加入订阅',
    message: activeKind.value === 'bangumi'
      ? `确定将所选 ${rows.length} 部 Bangumi 作品加入订阅计划吗？添加后会先匹配漫画或书籍内容源，未匹配时将在下次计划检查时重试。`
      : `确定订阅所选 ${rows.length} 部作品吗？将使用当前内容源默认下载目录：${downloadDir}${batchCoverBlur.value ? '，并开启封面模糊。' : '。'}`,
    confirmText: '批量订阅',
  })
  if (!confirmed) return
  batchSubscribing.value = true
  let succeeded = 0
  let failed = 0
  try {
    for (const item of rows) {
      try {
        await api.createSubscription({
          sourceId: item.sourceId,
          comicId: item.id,
          title: item.title,
          cover: item.cover,
          author: item.author,
          sourceUrl: item.sourceUrl,
          autoDownload: true,
          enabled: true,
          baseline: '',
          lastChapterOrder: 0,
          allChapters: activeKind.value !== 'bangumi',
          cronExpr: defaultSubscriptionCron(),
          downloadDir,
          seriesDir: '',
          convertToSimplified: true,
          chapterMarker: false,
          coverBlur: Boolean(batchCoverBlur.value),
        })
        succeeded += 1
      } catch {
        failed += 1
      }
    }
  } finally {
    batchSubscribing.value = false
  }
  if (downloadDir) rememberSubscriptionDownloadDir(downloadDir)
  clearDiscoverSelection()
  notify(
    failed
      ? `批量订阅完成：成功 ${succeeded} 项，失败 ${failed} 项`
      : `已批量订阅 ${succeeded} 部作品`,
    failed ? 'warning' : 'success',
  )
}

// 进入详情前把列表卡片数据留下来，详情页可以立刻渲染首屏。
function openComic(item) {
  rememberComicPreview(item)
  rememberPosition()
}

function latestLabel(item) {
  if (isBangumiItem(item)) return String(item.status || 'Bangumi')
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
          <button type="button" :class="{ active: activeKind === 'bangumi' }" @click="switchKind('bangumi')">
            <Sparkles :size="18" />
            Bangumi <span class="segmented-count">{{ kindCount('bangumi') }}</span>
          </button>
          <button type="button" :class="{ active: activeKind === 'comic' }" @click="switchKind('comic')">
            <ComicSourceIcon :size="18" />
            漫画源 <span class="segmented-count">{{ kindCount('comic') }}</span>
          </button>
          <button type="button" :class="{ active: activeKind === 'book' }" @click="switchKind('book')">
            <BookMarked :size="18" />
            书籍源 <span class="segmented-count">{{ kindCount('book') }}</span>
          </button>
          <button type="button" :class="{ active: activeKind === 'other' }" @click="switchKind('other')">
            <Film :size="18" />
            视频源 <span class="segmented-count">{{ kindCount('other') }}</span>
          </button>
        </div>
      </div>
      <div v-if="activeSource && activePanel" class="discover-search">
        <button
          class="discover-refresh-button"
          type="button"
          :disabled="activePanel.loading"
          aria-label="刷新当前内容源"
          title="刷新当前内容源和筛选"
          @click="refreshCurrentPanel"
        >
          <RefreshCw :size="18" :class="{ spin: activePanel.loading }" />
        </button>
        <label v-if="activeSource.canSearch" class="discover-search-field">
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
        @scroll="handleSourceStripScroll"
        @wheel="handleStripWheel"
      >
        <button
          v-for="source in sources"
          :key="source.id"
          type="button"
          :data-name="source.name"
          :title="source.name"
          :aria-label="source.name"
          :class="{ active: source.id === activeId }"
          @click="selectSource(source.id)"
        >
          <BangumiFeedIcon v-if="source.kind === 'bangumi'" class="source-tab-icon" :feed="source.feed" :size="18" />
          <SourceIcon v-else class="source-tab-icon" :src="source.icon" :kind="kindOf(source)" :alt="source.name" :size="18" />
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
    <div v-else-if="loadingSources" class="empty">
      <Loader2 :size="22" class="spin" />
      <span>正在加载资源</span>
    </div>
    <div v-else-if="!sources.length" class="card empty">
      <ImageOff :size="26" />
      <span>暂无可用的{{ sourceKindLabel(activeKind) }}，请先在资源拓展中启用来源。</span>
    </div>

    <section v-else-if="activeSource && activePanel" class="source-page">

      <div v-if="activeKind !== 'bangumi'" class="source-column-tools">
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
      <div v-else class="source-results-stage">
        <div
          v-if="viewMode === 'grid' && (activePanel.loading || visibleItems.length)"
          ref="gridRef"
          class="source-comic-grid source-comic-grid-7"
          :class="{ 'refresh-reveal': refreshRevealing }"
        >
          <ContentLink
            v-for="(item, index) in visibleItems"
            :key="`${item.sourceId}-${item.id}`"
            class="comic-card source-comic-card"
            :class="{ selected: isDiscoverSelected(item), selecting: discoverSelecting }"
            :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.id } }"
            :before-navigate="() => beforeDiscoverNavigate(item)"
            @click="handleDiscoverCardClick($event, item, index)"
            @contextmenu.prevent="enterDiscoverSelection($event, item, index)"
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
          <div
            v-for="index in loadingPlaceholderCount"
            :key="`grid-placeholder-${index}`"
            class="comic-card source-comic-card source-placeholder-card"
            aria-hidden="true"
          >
            <div class="comic-cover source-placeholder-cover"></div>
            <div class="comic-body">
              <span class="source-placeholder-line"></span>
              <span class="source-placeholder-line is-short"></span>
            </div>
          </div>
        </div>
        <div
          v-else-if="viewMode === 'list' && (activePanel.loading || visibleItems.length)"
          class="source-list-view"
          :class="{ 'refresh-reveal': refreshRevealing }"
        >
          <ContentLink
            v-for="(item, index) in visibleItems"
            :key="`${item.sourceId}-${item.id}`"
            class="source-list-card"
            :class="{ selected: isDiscoverSelected(item), selecting: discoverSelecting }"
            :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.id } }"
            :before-navigate="() => beforeDiscoverNavigate(item)"
            @click="handleDiscoverCardClick($event, item, index)"
            @contextmenu.prevent="enterDiscoverSelection($event, item, index)"
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
          <div
            v-for="index in loadingPlaceholderCount"
            :key="`list-placeholder-${index}`"
            class="source-list-card source-list-placeholder"
            aria-hidden="true"
          >
            <div class="source-list-cover source-placeholder-cover"></div>
            <div class="source-list-body">
              <span class="source-placeholder-line"></span>
              <span class="source-placeholder-line is-short"></span>
              <span class="source-placeholder-line"></span>
            </div>
          </div>
        </div>
        <div v-else class="source-empty">
          <ImageOff :size="24" />
          <span>没有作品</span>
        </div>
      </div>

      <div v-if="!activePanel.loading && activePanel.items.length" class="source-pagination">
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

  <div v-if="discoverSelecting" class="selection-pill selection-pill-flat discover-selection-bar">
    <span class="selection-count">已选 {{ selectedItems.size }} 项</span>
    <button class="btn ghost small" type="button" @click="toggleAllDiscoverItems">
      {{ selectedItems.size === visibleItems.length ? '取消全选' : '全选' }}
    </button>
    <button class="btn ghost small" type="button" @click="clearDiscoverSelection">
      <X :size="14" />
      清除
    </button>
    <button
      class="btn ghost small"
      :class="{ active: batchCoverBlur }"
      type="button"
      @click="batchCoverBlur = !batchCoverBlur"
    >
      <EyeOff :size="14" />
      封面模糊
    </button>
    <button
      class="btn ghost small"
      type="button"
      :disabled="!selectedItems.size || !canBatchSubscribe || batchSubscribing"
      :title="canBatchSubscribe ? '批量加入订阅' : '当前内容源未设置默认下载目录'"
      @click="batchSubscribeSelected"
    >
      <Loader2 v-if="batchSubscribing" :size="14" class="spin" />
      <Rss v-else :size="14" />
      订阅
    </button>
  </div>
</template>
