<script setup>
import { computed, nextTick, onDeactivated, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  ArrowDown,
  ArrowUp,
  BookOpen,
  Boxes,
  Check,
  ChevronLeft,
  ChevronRight,
  Download,
  Film,
  Languages,
  Layers3,
  ListChecks,
  Loader2,
  Merge,
  RefreshCw,
  Search,
  Tag,
  Trash2,
  UserRound,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import BackArrowIcon from '../components/BackArrowIcon.vue'
import CollectionEmptyState from '../components/CollectionEmptyState.vue'
import SortMenu from '../components/SortMenu.vue'
import WindowedGrid from '../components/WindowedGrid.vue'
import { askConfirm } from '../stores/confirm'
import { notify } from '../stores/notices'
import { dirBasename, splitAuthors } from '../utils/metadata'
import { paginateTextPages } from '../utils/reader'
import { SOURCES_CHANGED_EVENT } from '../utils/sourceEvents'

const items = ref([])
const libraryCounts = ref({ all: 0, comic: 0, book: 0, other: 0 })
const loading = ref(true)
const query = ref('')
const kind = ref('all')
const active = ref(null)
const detailLoading = ref(false)
let detailGeneration = 0
const reader = ref(null)
const readerLoading = ref(false)
const readerPageIndex = ref(0)
const readerCBZHTML = ref('')
const readerCBZDocumentLoading = ref(false)
let readerCBZRequestSeq = 0
const selectionMode = ref(false)
const selectedPaths = ref([])
const selectedSnapshots = ref(new Map())
const selectionAnchor = ref(-1)
const batchWorking = ref(false)
const chapterQuery = ref('')
const chapterApplied = ref('')
const chapterSortMode = ref('title')
const chapterSortDesc = ref(false)
// 详情页排序改用共用组件 SortMenu。
const chapterSortOptions = [
  { value: 'title', label: '标题' },
  { value: 'time', label: '添加时间' },
]
const rootOrder = ref([])
const activeRoot = ref('')
const heroTint = ref('')

// 本地仓库排序：默认标题升序，排序字段与方向组合持久化。
const SORT_STORAGE_KEY = 'manco.library.sort.v1'
const sortOptions = [
  { value: 'created', label: '添加时间' },
  { value: 'title', label: '标题' },
]
const sortMode = ref('title')
const sortDesc = ref(false)
try {
  const saved = JSON.parse(window.localStorage.getItem(SORT_STORAGE_KEY) || 'null')
  const savedModeValid = saved && sortOptions.some((option) => option.value === saved.mode)
  if (savedModeValid) {
    sortMode.value = saved.mode
    if (typeof saved.desc === 'boolean') sortDesc.value = saved.desc
  }
} catch {
  // 忽略损坏的本地设置
}
watch([sortMode, sortDesc], () => {
  try {
    window.localStorage.setItem(SORT_STORAGE_KEY, JSON.stringify({ mode: sortMode.value, desc: sortDesc.value }))
  } catch {
    // 隐私模式下写不进去，忽略
  }
})

function onSortChange({ mode, desc }) {
  sortMode.value = mode
  sortDesc.value = desc
}

// 服务端按当前筛选和排序返回一个数据窗口，前端不再累积全部作品。
const filtered = computed(() => items.value)

// 本地仓库目录栏目：按订阅（添加订阅的先后）顺序展示作品父目录。
function rootOf(item) {
  return String(item?.path || '').split('|')[0] || ''
}

function rootLabel(root) {
  return dirBasename(root) || root || '下载目录'
}

const libraryRoots = computed(() => rootOrder.value.filter(Boolean))

const comicCount = computed(() => libraryCounts.value.comic || 0)
const bookCount = computed(() => libraryCounts.value.book || 0)
const otherCount = computed(() => libraryCounts.value.other || 0)

const activeFiles = computed(() => active.value?.files || [])
const activeAuthors = computed(() => splitAuthors(active.value?.author))
const activeChapterFiles = computed(() => {
  const keyword = chapterApplied.value.trim().toLowerCase()
  const list = (activeFiles.value || []).filter((file) => {
    if (!keyword) return true
    return String(file.name || '').toLowerCase().includes(keyword)
  })
  const rows = [...list]
  rows.sort((a, b) => {
    let result
    if (chapterSortMode.value === 'time') {
      const left = a.modTime ? new Date(a.modTime).getTime() : 0
      const right = b.modTime ? new Date(b.modTime).getTime() : 0
      result = left - right
    } else {
      result = String(a.name || '').localeCompare(String(b.name || ''), 'zh-Hans-CN', { numeric: true })
    }
    return chapterSortDesc.value ? -result : result
  })
  return rows
})
const heroStyle = computed(() => ({
  '--hero-tint': heroTint.value || 'transparent',
  '--hero-cover': active.value?.cover ? 'url("' + coverUrl(active.value) + '")' : 'none',
}))

const readerTitle = computed(() => reader.value?.file?.name || '')
const readerImages = computed(() => reader.value?.images || [])
const readerCBZPages = computed(() => reader.value?.cbzPages || [])
const readerText = computed(() => reader.value?.text || '')
const readerTextPages = computed(() => paginateTextPages(readerText.value))
const readerEpubChapters = computed(() =>
  (reader.value?.epubChapters || []).map((chapter) => ({
    ...chapter,
    html: rewriteEpubAssetUrls(chapter.html),
  })),
)
const readerVideo = computed(() => Boolean(reader.value?.video))
const readerPdf = computed(() => Boolean(reader.value?.pdf))
const currentReaderCBZPage = computed(() => readerCBZPages.value[readerPageIndex.value] || null)
const currentReaderCBZImage = computed(() =>
  currentReaderCBZPage.value?.kind === 'image' ? currentReaderCBZPage.value : null,
)
const currentReaderCBZDocument = computed(() =>
  currentReaderCBZPage.value?.kind === 'document' ? currentReaderCBZPage.value : null,
)
const readerPageCount = computed(() => {
  if (readerVideo.value || readerPdf.value) return reader.value?.file ? 1 : 0
  if (readerCBZPages.value.length) return readerCBZPages.value.length
  if (readerImages.value.length) return readerImages.value.length
  if (readerEpubChapters.value.length) return readerEpubChapters.value.length
  return readerTextPages.value.length
})
const currentReaderImage = computed(() => {
  if (readerCBZPages.value.length) return currentReaderCBZImage.value
  return readerImages.value[readerPageIndex.value] || null
})
const currentReaderEpubChapter = computed(() => readerEpubChapters.value[readerPageIndex.value] || null)
const currentReaderText = computed(() => readerTextPages.value[readerPageIndex.value] || '')
const selectedItems = computed(() =>
  selectedPaths.value.map((path) => selectedSnapshots.value.get(path)).filter(Boolean),
)
const selectedCount = computed(() => selectedItems.value.length)
const selectedFileCount = computed(() =>
  selectedItems.value
    .filter((item) => keyOf(item) !== selectedPaths.value[0])
    .reduce((total, item) => total + Number(item.totalFiles || item.files?.length || 0), 0),
)

onMounted(() => {
  load()
  window.addEventListener('keydown', handleKeydown)
  window.addEventListener(SOURCES_CHANGED_EVENT, refreshSourceKinds)
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleKeydown)
  window.removeEventListener(SOURCES_CHANGED_EVENT, refreshSourceKinds)
  if (queryTimer) window.clearTimeout(queryTimer)
  libraryController?.abort()
})

async function refreshSourceKinds() {
  await load()
}

// 切走页面时退出多选。
onDeactivated(() => {
  clearSelection()
  libraryController?.abort()
})

// 本地仓库数据窗口：只保留可视区域及上下各一行，离开窗口的数据直接释放。
const totalItems = ref(0)
const libraryRangeEnd = ref(0)
const libraryOffset = ref(0)
const libraryWindowLoading = ref(false)
let libraryRequestGeneration = 0
let libraryController = null
let queryTimer = 0
const libraryHasMore = computed(() => libraryRangeEnd.value < totalItems.value)

function estimatedLibraryColumns() {
  if (window.innerWidth <= 760) return 2
  if (window.innerWidth <= 1100) return 4
  if (window.innerWidth <= 1400) return 5
  return 7
}

function initialLibraryLimit() {
  const viewport = document.querySelector('.content-scroll')?.clientHeight || Math.max(320, window.innerHeight - 180)
  const columns = estimatedLibraryColumns()
  const rowHeight = columns <= 2 ? 360 : columns <= 4 ? 300 : 220
  const rows = Math.max(2, Math.ceil(viewport / rowHeight) + 1)
  return rows * columns
}

function libraryRequestOptions() {
  return {
    query: query.value.trim(),
    kind: kind.value,
    root: activeRoot.value,
    sort: sortMode.value,
    desc: sortDesc.value,
  }
}

async function fetchLibraryWindow(start = 0, limit = initialLibraryLimit(), initial = false, append = false) {
  const generation = ++libraryRequestGeneration
  libraryController?.abort()
  libraryController = new AbortController()
  if (initial) loading.value = true
  else libraryWindowLoading.value = true
  try {
    const payload = await api.localLibrary(Math.max(1, limit), Math.max(0, start), true, {
      ...libraryRequestOptions(),
      signal: libraryController.signal,
    })
    if (generation !== libraryRequestGeneration) return
    const next = payload.items || []
    if (append) {
      const existing = new Set(items.value.map(keyOf))
      items.value = [...items.value, ...next.filter((item) => !existing.has(keyOf(item)))]
      libraryRangeEnd.value = Math.max(libraryRangeEnd.value, start + next.length)
    } else {
      items.value = next
      libraryOffset.value = Math.max(0, start)
      libraryRangeEnd.value = Math.max(libraryRangeEnd.value, start + next.length)
    }
    for (const item of items.value) {
      const path = keyOf(item)
      if (selectedPaths.value.includes(path)) selectedSnapshots.value.set(path, item)
    }
    totalItems.value = Number(payload.total || 0)
    libraryCounts.value = payload.counts || { all: totalItems.value, comic: 0, book: 0, other: 0 }
    rootOrder.value = Array.isArray(payload.downloadDirs) ? payload.downloadDirs : rootOrder.value
    if (activeRoot.value && !rootOrder.value.includes(activeRoot.value)) activeRoot.value = ''
  } catch (err) {
    if (err.name !== 'AbortError' && generation === libraryRequestGeneration) {
      notify(`加载本地仓库失败：${err.message}`, true)
    }
  } finally {
    if (generation === libraryRequestGeneration) {
      loading.value = false
      libraryWindowLoading.value = false
    }
  }
}

function onLibraryWindowChange({ start, limit }) {
  if (active.value || libraryWindowLoading.value) return
  if (start === libraryOffset.value && limit === items.value.length) return
  fetchLibraryWindow(start, limit)
}

function loadMoreLibrary() {
  if (!libraryHasMore.value || libraryWindowLoading.value) return
  fetchLibraryWindow(libraryRangeEnd.value, initialLibraryLimit(), false, true)
}

async function load() {
  loading.value = true
  libraryRangeEnd.value = 0
  try {
    await fetchLibraryWindow(0, initialLibraryLimit(), true)
  } catch (err) {
    notify(`本地仓库加载失败：${err.message}`, true)
    loading.value = false
  }
}

function reloadCurrentWindow() {
  clearSelection()
  const scroller = document.querySelector('.content-scroll')
  if (scroller) scroller.scrollTop = 0
  libraryRangeEnd.value = 0
  load()
}

watch([kind, activeRoot, sortMode, sortDesc], reloadCurrentWindow)
watch(query, () => {
  if (queryTimer) window.clearTimeout(queryTimer)
  queryTimer = window.setTimeout(reloadCurrentWindow, 260)
})

function keyOf(item) {
  return item.path || item.title
}

function isSelected(item) {
  return selectedPaths.value.includes(keyOf(item))
}

function itemAtGlobalIndex(index) {
  return items.value[index - libraryOffset.value] || null
}

function rememberSelection(item) {
  const path = keyOf(item)
  if (!selectedPaths.value.includes(path)) selectedPaths.value = [...selectedPaths.value, path]
  selectedSnapshots.value.set(path, item)
}

function enterSelection(item, index, event) {
  event?.preventDefault()
  selectionMode.value = true
  rememberSelection(item)
  selectionAnchor.value = index
}

function handleCardClick(item, index, event) {
  if (!selectionMode.value) {
    openDetail(item)
    return
  }
  if (event.shiftKey && selectionAnchor.value >= 0) {
    const start = Math.min(selectionAnchor.value, index)
    const end = Math.max(selectionAnchor.value, index)
    const next = new Set(selectedPaths.value)
    for (let cursor = start; cursor <= end; cursor += 1) {
      const entry = itemAtGlobalIndex(cursor)
      if (!entry) continue
      next.add(keyOf(entry))
      selectedSnapshots.value.set(keyOf(entry), entry)
    }
    selectedPaths.value = [...next]
    selectionAnchor.value = index
    return
  }
  toggleSelection(item)
  selectionAnchor.value = index
}

function toggleSelection(item) {
  const path = keyOf(item)
  if (selectedPaths.value.includes(path)) {
    selectedPaths.value = selectedPaths.value.filter((value) => value !== path)
    selectedSnapshots.value.delete(path)
  } else {
    rememberSelection(item)
  }
}

function selectAll() {
  selectedPaths.value = filtered.value.map(keyOf)
  selectedSnapshots.value = new Map(filtered.value.map((item) => [keyOf(item), item]))
  selectionMode.value = selectedPaths.value.length > 0
  selectionAnchor.value = selectedPaths.value.length ? 0 : -1
}

function clearSelection() {
  selectionMode.value = false
  selectedPaths.value = []
  selectedSnapshots.value = new Map()
  selectionAnchor.value = -1
}

function handleKeydown(event) {
  if (reader.value) {
    if (event.key === 'Escape') {
      closeReader()
      return
    }
    const tagName = String(event.target?.tagName || '').toUpperCase()
    if (tagName === 'VIDEO' || tagName === 'AUDIO' || ['INPUT', 'TEXTAREA', 'SELECT'].includes(tagName)) return
    if (event.key === 'ArrowLeft') {
      event.preventDefault()
      stepReaderPage(-1)
    } else if (event.key === 'ArrowRight') {
      event.preventDefault()
      stepReaderPage(1)
    }
    return
  }
  if (event.key === 'Escape' && selectionMode.value) clearSelection()
}

async function simplifySelection() {
  if (!selectedCount.value || batchWorking.value) return
  batchWorking.value = true
  try {
    const payload = await api.localSimplify(selectedPaths.value)
    notify(`繁转简完成，共处理 ${payload.renamed || 0} 个名称`)
    clearSelection()
    await load()
  } catch (err) {
    notify(`繁转简失败：${err.message}`, true)
  } finally {
    batchWorking.value = false
  }
}

async function mergeSelection() {
  if (selectedCount.value < 2 || batchWorking.value) {
    if (selectedCount.value < 2) notify('至少选择两部作品才能合并', true)
    return
  }
  const parent = selectedItems.value.find((item) => keyOf(item) === selectedPaths.value[0]) || selectedItems.value[0]
  const confirmed = await askConfirm({
    title: '合并本地作品',
    message: `将把 ${selectedFileCount.value} 个文件合并到母本文件夹「${parent.title}」。确认后才会执行，是否继续？`,
    confirmText: '确认合并',
    danger: false,
  })
  if (!confirmed) return
  batchWorking.value = true
  try {
    const payload = await api.localMerge(selectedPaths.value)
    notify(`已将 ${payload.movedFiles || 0} 个文件合并到「${payload.parent || parent.title}」`)
    clearSelection()
    await load()
  } catch (err) {
    notify(`合并失败：${err.message}`, true)
  } finally {
    batchWorking.value = false
  }
}

async function deleteSelection() {
  if (!selectedCount.value || batchWorking.value) return
  const confirmed = await askConfirm({
    title: '删除本地作品',
    message: `将删除所选 ${selectedCount.value} 部作品及其目录中的全部文件，删除后无法恢复。是否继续？`,
    confirmText: '确认删除',
    danger: true,
  })
  if (!confirmed) return
  batchWorking.value = true
  try {
    const payload = await api.localDelete(selectedPaths.value)
    notify(`已删除 ${payload.deletedItems || 0} 部作品，共 ${payload.deletedFiles || 0} 个文件`)
    clearSelection()
    await load()
  } catch (err) {
    notify(`删除失败：${err.message}`, true)
  } finally {
    batchWorking.value = false
  }
}

function coverUrl(item) {
    if (item.cover) return api.localFileUrl(item.cover, false, 320)
  if (item.cachedCover) return item.cachedCover
    if (item.sourceCover) return api.imageUrl(item.sourceCover, item.sourceId, '', 320)
  return ''
}

function fileUrl(file, download = false) {
  return api.localFileUrl(file.path, download)
}

function chapterName(file) {
  return String(file.name || '').replace(/\.[^.]+$/, '')
}

function chapterCount(item) {
  return Number(item.chapterCount ?? item.files?.length ?? item.totalFiles ?? 0)
}

function chapterThumbnail(file) {
  return file.ext === 'cbz' || file.ext === 'zip' ? api.localChapterThumbnailUrl(file) : ''
}

function extractTint(url) {
  try {
    const image = new Image()
    image.crossOrigin = 'anonymous'
    image.onload = () => {
      try {
        const canvas = document.createElement('canvas')
        canvas.width = 20
        canvas.height = 30
        const context = canvas.getContext('2d')
        context.drawImage(image, 0, 0, 20, 30)
        const { data } = context.getImageData(0, 0, 20, 30)
        let r = 0
        let g = 0
        let b = 0
        let count = 0
        for (let i = 0; i < data.length; i += 4) {
          r += data[i]
          g += data[i + 1]
          b += data[i + 2]
          count += 1
        }
        if (count) {
          heroTint.value = 'rgb(' + Math.round(r / count) + ', ' + Math.round(g / count) + ', ' + Math.round(b / count) + ')'
        }
      } catch {
        heroTint.value = ''
      }
    }
    image.src = url
  } catch {
    heroTint.value = ''
  }
}

// SortMenu 组件回传 { mode, desc }。
function onChapterSortChange({ mode, desc }) {
  chapterSortMode.value = mode
  chapterSortDesc.value = desc
}

async function openDetail(item) {
  const generation = ++detailGeneration
  active.value = item
  detailLoading.value = true
  await nextTick()
  const scroller = document.querySelector('.content-scroll')
  if (scroller) scroller.scrollTop = 0
  heroTint.value = ''
  if (item.cover) extractTint(coverUrl(item))
  chapterQuery.value = ''
  chapterApplied.value = ''
  chapterSortMode.value = 'title'
  chapterSortDesc.value = false
  try {
    const payload = await api.localItem(item.path)
    if (generation === detailGeneration && active.value) {
      active.value = { ...item, ...payload.item }
    }
  } catch (err) {
    if (generation === detailGeneration) notify(`章节加载失败：${err.message}`, true)
  } finally {
    if (generation === detailGeneration) detailLoading.value = false
  }
}

function closeDetail() {
  detailGeneration += 1
  detailLoading.value = false
  active.value = null
}

function applyChapterSearch() {
  chapterApplied.value = chapterQuery.value
}

function clearChapterSearch() {
  chapterQuery.value = ''
  chapterApplied.value = ''
}

async function openReader(file) {
  if (file.kind === 'other') {
    readerPageIndex.value = 0
    reader.value = { file, video: true, images: [], cbzPages: [], text: '', epubChapters: [] }
    return
  }
  readerLoading.value = true
  readerPageIndex.value = 0
  try {
    if (file.ext === 'cbz' || file.ext === 'zip') {
      const payload = await api.localCBZ(file.path)
      const pages = Array.isArray(payload.pages) && payload.pages.length
        ? payload.pages
        : (payload.items || []).map((entry) => ({ ...entry, kind: 'image' }))
      reader.value = {
        file,
        images: payload.items || [],
        cbzPages: pages,
        text: '',
        epubChapters: [],
      }
    } else if (file.ext === 'epub') {
      const payload = await api.localEPUB(file.path)
      reader.value = { file, images: [], text: '', epubChapters: payload.items || [] }
    } else if (file.ext === 'pdf') {
      reader.value = { file, pdf: true, images: [], cbzPages: [], text: '', epubChapters: [] }
    } else {
      reader.value = { file, images: [], text: '', epubChapters: [] }
      const response = await fetch(api.localFileUrl(file.path))
      if (!response.ok) throw new Error(`无法读取文件（${response.status}）`)
      reader.value = { file, images: [], text: await response.text(), epubChapters: [] }
    }
  } catch (err) {
    reader.value = null
    notify(`打开失败：${err.message}`, true)
  } finally {
    readerLoading.value = false
  }
}

function closeReader() {
  readerCBZRequestSeq += 1
  reader.value = null
  readerPageIndex.value = 0
  readerCBZHTML.value = ''
  readerCBZDocumentLoading.value = false
}

function readerImageUrl(entry) {
  return api.localCBZFileUrl(reader.value.file.path, entry.name)
}

function rewriteCBZAssetUrls(html) {
  if (!html || !reader.value?.file?.path) return html || ''
  return html.replace(/\bsrc=(["'])([^"']+)\1/gi, (match, quote, source) => {
    const value = String(source || '').trim()
    if (!value || /^(?:data:|https?:|blob:|\/)/i.test(value)) return match
    return `src=${quote}${api.localCBZFileUrl(reader.value.file.path, value.replace(/^\.\//, ''))}${quote}`
  })
}

function stepReaderPage(delta) {
  if (readerPageCount.value <= 1) return
  readerPageIndex.value = Math.min(
    readerPageCount.value - 1,
    Math.max(0, readerPageIndex.value + delta),
  )
}

watch(currentReaderCBZDocument, async (page) => {
  const requestSeq = ++readerCBZRequestSeq
  readerCBZHTML.value = ''
  readerCBZDocumentLoading.value = false
  if (!page || !reader.value?.file?.path) return
  readerCBZDocumentLoading.value = true
  try {
    const payload = await api.localCBZDocument(reader.value.file.path, page.name)
    if (requestSeq === readerCBZRequestSeq) {
      readerCBZHTML.value = rewriteCBZAssetUrls(payload.html)
    }
  } catch (err) {
    if (requestSeq === readerCBZRequestSeq) notify(`打开书籍页面失败：${err.message}`, true)
  } finally {
    if (requestSeq === readerCBZRequestSeq) readerCBZDocumentLoading.value = false
  }
}, { immediate: true })

function rewriteEpubAssetUrls(html) {
  if (!html) return ''
  const path = reader.value?.file?.path || ''
  return html.replace(/\/api\/local\/epub\/file\?entry=([^"')>\s]+)/g, (match, entry) => {
    let decoded = entry
    try {
      decoded = decodeURIComponent(entry)
    } catch {
      decoded = entry
    }
    return api.localEPUBFileUrl(path, decoded)
  })
}

function downloadReaderFile() {
  if (!reader.value) return
  window.location.href = api.localFileUrl(reader.value.file.path, true)
}
</script>

<template>
  <div v-if="active" class="local-detail-view" :style="heroStyle">
    <div class="detail-hero local-detail-hero">
      <button class="detail-cover-back" type="button" aria-label="返回本地仓库" @click="closeDetail">
        <BackArrowIcon />
      </button>
      <div class="detail-cover-wrap">
        <div class="detail-cover">
          <img v-if="coverUrl(active)" :src="coverUrl(active)" :alt="active.title" />
          <span v-else class="cover-fallback"><BookOpen :size="30" /></span>
        </div>
      </div>
      <div class="detail-info">
        <div v-if="active" class="detail-dir-label">{{ rootLabel(rootOf(active)) }}</div>
        <div class="detail-info-head">
          <h2>{{ active.title }}</h2>
        </div>
        <div class="meta detail-meta-line">
          <template v-if="activeAuthors.length">
            <span v-for="name in activeAuthors" :key="name" class="author-pill">
              <UserRound :size="12" :stroke-width="1.9" />
              {{ name }}
            </span>
          </template>
          <span v-else>未知作者</span>
        </div>
        <div v-if="active.tags?.length" class="tag-list">
          <span v-for="tag in active.tags" :key="tag" class="badge tag-pill">
            <Tag :size="12" />{{ tag }}
          </span>
        </div>
        <p class="description">{{ active.description || '暂无简介' }}</p>
      </div>
    </div>

    <section class="detail-chapters">
      <div class="detail-chapter-head">
        <span class="muted small">共 {{ activeChapterFiles.length }} 项</span>
        <span class="spacer" />
        <form class="chapter-search" @submit.prevent="applyChapterSearch">
          <Search :size="15" />
          <input v-model="chapterQuery" class="input" placeholder="搜索章节" @keyup.enter="applyChapterSearch" />
          <button v-if="chapterQuery" class="icon-btn" type="button" aria-label="清除" @click="clearChapterSearch">
            <X :size="14" />
          </button>
        </form>
        <SortMenu
          :options="chapterSortOptions"
          :mode="chapterSortMode"
          :desc="chapterSortDesc"
          @change="onChapterSortChange"
        />
      </div>

      <div v-if="detailLoading" class="empty"><Loader2 :size="20" class="spin" /></div>
      <div v-else-if="!activeChapterFiles.length" class="empty">没有可阅读的章节</div>
      <WindowedGrid v-else class="local-chapter-poster-grid" :items="activeChapterFiles">
        <template #default="{ item: file }">
        <button
          class="local-chapter-poster"
          type="button"
          :title="file.name"
          @click="openReader(file)"
        >
          <span class="local-chapter-poster-cover">
            <img v-if="chapterThumbnail(file)" :src="chapterThumbnail(file)" :alt="file.name" loading="lazy" decoding="async" />
          </span>
          <span class="local-chapter-poster-name">{{ chapterName(file) }}</span>
        </button>
        </template>
      </WindowedGrid>
    </section>
  </div>

  <div v-else>
    <div class="toolbar library-toolbar">
      <div class="segmented" role="tablist" aria-label="本地仓库类型">
        <button type="button" :class="{ active: kind === 'all' }" @click="kind = 'all'">
          <Layers3 :size="16" />
          全部
          <span class="segmented-count">{{ libraryCounts.all || 0 }}</span>
        </button>
        <button type="button" :class="{ active: kind === 'comic' }" @click="kind = 'comic'">
          <BookOpen :size="16" />
          漫画
          <span class="segmented-count">{{ comicCount }}</span>
        </button>
        <button type="button" :class="{ active: kind === 'book' }" @click="kind = 'book'">
          <Film :size="16" />
          书籍
          <span class="segmented-count">{{ bookCount }}</span>
        </button>
        <button type="button" :class="{ active: kind === 'other' }" @click="kind = 'other'">
          <Boxes :size="16" />
          视频源
          <span class="segmented-count">{{ otherCount }}</span>
        </button>
      </div>
      <span class="spacer" />
      <button
        class="icon-btn library-refresh-button"
        type="button"
        :disabled="loading"
        aria-label="刷新本地仓库"
        title="刷新本地仓库"
        @click="load"
      >
        <RefreshCw :size="17" :class="{ spin: loading }" />
      </button>
      <label class="search-field local-search">
        <Search :size="16" />
        <input v-model="query" aria-label="筛选本地作品" placeholder="搜索作品或作者" />
      </label>
    </div>

    <div v-if="libraryRoots.length" class="local-root-row">
      <div class="local-root-strip" aria-label="本地仓库目录">
      <button type="button" :class="{ active: !activeRoot }" @click="activeRoot = ''">全部</button>
      <button
        v-for="root in libraryRoots"
        :key="root"
        type="button"
        :class="{ active: activeRoot === root }"
        @click="activeRoot = root"
      >
        {{ rootLabel(root) }}
      </button>
      </div>
      <SortMenu
        class="local-library-sort"
        :options="sortOptions"
        :mode="sortMode"
        :desc="sortDesc"
        @change="onSortChange"
      />
    </div>

    <div v-if="loading && !items.length" class="empty">
      <Loader2 :size="22" class="spin" />
      <span>正在读取本地仓库</span>
    </div>
    <CollectionEmptyState
      v-else-if="!totalItems && !query.trim() && !activeRoot && kind === 'all'"
      class="page-empty-state"
      variant="local"
      window-title="本地书架"
      heading="本地书架还是空的"
      description="下载完成的漫画、书籍和其他资源会收进这里。先让猫咪帮你守着一格空书架。"
    />
    <div v-else-if="!items.length" class="card empty">
      <Search :size="26" />
      <p>没有匹配的作品。</p>
    </div>
    <WindowedGrid
      v-else
      class="local-library-grid"
      :items="filtered"
      :total="libraryRangeEnd"
      :offset="libraryOffset"
      :loading-row="libraryHasMore"
      @window-change="onLibraryWindowChange"
      @near-end="loadMoreLibrary"
    >
      <template #default="{ item, index }">
      <button
        class="card local-library-card"
        :class="{ selected: isSelected(item), selecting: selectionMode }"
        type="button"
        @click="handleCardClick(item, index, $event)"
        @contextmenu.prevent="enterSelection(item, index, $event)"
      >
        <span v-if="isSelected(item)" class="local-selection-check"><Check :size="16" /></span>
        <span class="local-library-cover">
          <span class="local-library-chapter-badge" :class="{ single: String(chapterCount(item)).length === 1 }">
            {{ chapterCount(item) }}
          </span>
          <span class="local-library-cover-frame">
            <img v-if="coverUrl(item)" :src="coverUrl(item)" :alt="item.title" loading="lazy" decoding="async" />
            <span v-else class="cover-fallback"><BookOpen :size="30" /></span>
          </span>
        </span>
        <span class="local-library-body">
          <strong class="local-library-title">{{ item.title }}</strong>
          <span class="local-library-meta">
            <span>{{ item.author || '未知作者' }}</span>
          </span>
        </span>
      </button>
      </template>
      <template #loading="{ columns }">
        <div
          class="local-library-loading-row"
          :style="{ gridTemplateColumns: `repeat(${Math.max(1, columns || 1)}, minmax(0, 1fr))` }"
        >
          <span v-for="index in Math.max(1, columns || 1)" :key="index" class="local-library-skeleton-card">
            <span class="local-library-skeleton-cover" />
            <span class="local-library-skeleton-line" />
            <span class="local-library-skeleton-line short" />
          </span>
        </div>
      </template>
    </WindowedGrid>
    <div v-if="selectionMode" class="local-selection-bar">
      <span class="local-selection-count"><ListChecks :size="16" /> 已选 {{ selectedCount }} 项</span>
      <button class="btn secondary small" type="button" :disabled="batchWorking" @click="selectAll">全选</button>
      <button class="btn secondary small" type="button" :disabled="batchWorking" @click="clearSelection">清空</button>
      <button class="btn secondary small" type="button" :disabled="batchWorking || !selectedCount" @click="simplifySelection">
        <Languages :size="15" />
        繁转简
      </button>
      <button class="btn small" type="button" :disabled="batchWorking || selectedCount < 2" @click="mergeSelection">
        <Merge :size="15" />
        合并
      </button>
      <button class="btn danger small" type="button" :disabled="batchWorking || !selectedCount" @click="deleteSelection">
        <Trash2 :size="15" />
        删除
      </button>
    </div>
  </div>

  <div v-if="reader || readerLoading" class="modal-backdrop reader-backdrop" @click.self="closeReader">
    <div class="modal reader-modal">
      <div class="modal-head">
        <div>
          <h2>{{ readerTitle }}</h2>
          <p v-if="readerPdf" class="muted small">浏览器 PDF 阅读器</p>
          <p v-else-if="readerVideo" class="muted small">浏览器播放器</p>
          <p v-else-if="readerCBZPages.length" class="muted small">
            共 {{ readerCBZPages.length }} 页 · 翻页阅读
          </p>
          <p v-else-if="readerImages.length" class="muted small">
            共 {{ readerImages.length }} 页 · 翻页阅读
          </p>
          <p v-else-if="readerEpubChapters.length" class="muted small">
            共 {{ readerEpubChapters.length }} 章 · 翻页阅读
          </p>
          <p v-else-if="readerTextPages.length" class="muted small">
            共 {{ readerTextPages.length }} 页 · 翻页阅读
          </p>
        </div>
        <div class="inline">
          <button class="btn secondary small" type="button" @click="downloadReaderFile">
            <Download :size="15" />
            下载
          </button>
          <button class="btn ghost icon" type="button" aria-label="关闭阅读器" @click="closeReader">
            <X :size="18" />
          </button>
        </div>
      </div>

      <div v-if="readerLoading" class="empty">
        <Loader2 :size="24" class="spin" />
        <span>正在打开</span>
      </div>
      <template v-else-if="reader">
        <div v-if="readerVideo && reader.file" class="preview-video-shell">
          <video
            class="browser-video-player"
            :src="api.localFileUrl(reader.file.path)"
            controls
            playsinline
            preload="metadata"
          />
          <a class="btn secondary small" :href="api.localFileUrl(reader.file.path)" target="_blank" rel="noreferrer">
            新窗口播放
          </a>
        </div>
        <iframe
          v-else-if="readerPdf && reader.file"
          class="browser-document-frame"
          :src="api.localFileUrl(reader.file.path)"
          :title="readerTitle"
        />
        <div v-else-if="currentReaderImage" class="reader-page-stage">
          <img
            :key="currentReaderImage.name"
            :src="readerImageUrl(currentReaderImage)"
            :alt="`第 ${readerPageIndex + 1} 页`"
            loading="eager"
            decoding="async"
          />
        </div>
        <article v-else-if="currentReaderCBZDocument" class="epub-reader reader-page-stage">
          <div v-if="readerCBZDocumentLoading" class="empty">
            <Loader2 :size="24" class="spin" />
            <span>正在打开书籍页面</span>
          </div>
          <div v-else-if="readerCBZHTML" class="epub-chapter-body" v-html="readerCBZHTML"></div>
          <div v-else class="empty">
            <span>（空章节）</span>
          </div>
        </article>
        <article v-else-if="currentReaderEpubChapter" class="epub-reader reader-page-stage">
          <section class="epub-chapter">
            <!-- Content comes from the user's own downloaded EPUB and is sanitized by the backend. -->
            <div class="epub-chapter-body" v-html="currentReaderEpubChapter.html"></div>
          </section>
        </article>
        <article v-else class="reader-text reader-page-text">
          {{ currentReaderText || '（空文件）' }}
        </article>
        <div v-if="readerPageCount > 1" class="reader-pager">
          <button
            class="btn secondary small"
            type="button"
            :disabled="readerPageIndex <= 0"
            aria-label="上一页"
            @click="stepReaderPage(-1)"
          >
            <ChevronLeft :size="17" />
            上一页
          </button>
          <span>{{ readerPageIndex + 1 }} / {{ readerPageCount }}</span>
          <button
            class="btn secondary small"
            type="button"
            :disabled="readerPageIndex >= readerPageCount - 1"
            aria-label="下一页"
            @click="stepReaderPage(1)"
          >
            下一页
            <ChevronRight :size="17" />
          </button>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
@media (max-width: 600px) {
  .local-detail-hero {
    grid-template-columns: 110px minmax(0, 1fr);
    gap: 16px;
  }
  .local-detail-hero .detail-cover-wrap,
  .local-detail-hero .detail-cover { width: 110px; min-width: 0; }
  .local-detail-hero .detail-info { min-width: 0; }
  .local-detail-hero::before { left: 0; right: 0; }
  .local-detail-hero h2 { font-size: 24px; overflow-wrap: anywhere; }
  .detail-chapter-head { flex-wrap: wrap; gap: 10px; }
  .detail-chapter-head .chapter-search { min-width: 0; width: auto; flex: 1 1 150px; }
  .detail-chapter-head .chapter-search .input { width: 100%; min-width: 0; }
  .detail-chapter-head .spacer { display: none; }
}
</style>
