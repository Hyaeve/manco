<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  BookOpen,
  Check,
  Download,
  Film,
  FolderOpen,
  Languages,
  Layers3,
  ListChecks,
  Loader2,
  Merge,
  Search,
  Tag,
  Trash2,
  UserRound,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import { askConfirm } from '../stores/confirm'
import { notify } from '../stores/notices'

const items = ref([])
const loading = ref(true)
const query = ref('')
const kind = ref('all')
const active = ref(null)
const reader = ref(null)
const readerLoading = ref(false)
const selectionMode = ref(false)
const selectedPaths = ref([])
const selectionAnchor = ref(-1)
const batchWorking = ref(false)
const chapterQuery = ref('')
const chapterApplied = ref('')
const chapterSortMode = ref('title')
const chapterSortDesc = ref(false)
const chapterSortOpen = ref(false)
const chapterSortUpward = ref(false)
const chapterSortRoot = ref(null)
const rootOrder = ref([])
const activeRoot = ref('')
const heroTint = ref('')

const filtered = computed(() => {
  const keyword = query.value.trim().toLowerCase()
  return items.value.filter((item) => {
    if (kind.value !== 'all' && item.kind !== kind.value) return false
    if (activeRoot.value && rootOf(item) !== activeRoot.value) return false
    if (!keyword) return true
    return (
      (item.title || '').toLowerCase().includes(keyword) ||
      (item.author || '').toLowerCase().includes(keyword)
    )
  })
})

// 本地库目录栏目：按订阅（添加订阅的先后）顺序展示作品父目录。
function rootOf(item) {
  return String(item?.path || '').split('|')[0] || ''
}

function rootLabel(root) {
  const parts = String(root || '').split('/').filter(Boolean)
  return parts[parts.length - 1] || root || '下载目录'
}

const libraryRoots = computed(() => {
  const used = new Set(items.value.map((item) => rootOf(item)))
  const ordered = []
  const seen = new Set()
  for (const root of rootOrder.value) {
    if (!root || !used.has(root) || seen.has(root)) continue
    seen.add(root)
    ordered.push(root)
  }
  for (const item of items.value) {
    const root = rootOf(item)
    if (!root || seen.has(root)) continue
    seen.add(root)
    ordered.push(root)
  }
  return ordered
})

const comicCount = computed(() => items.value.filter((item) => item.kind !== 'book').length)
const bookCount = computed(() => items.value.filter((item) => item.kind === 'book').length)

const activeFiles = computed(() => active.value?.files || [])
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
const readerText = computed(() => reader.value?.text || '')
const selectedItems = computed(() => items.value.filter((item) => selectedPaths.value.includes(keyOf(item))))
const selectedCount = computed(() => selectedItems.value.length)
const selectedFileCount = computed(() =>
  selectedItems.value
    .filter((item) => keyOf(item) !== selectedPaths.value[0])
    .reduce((total, item) => total + Number(item.totalFiles || item.files?.length || 0), 0),
)

onMounted(() => {
  load()
  window.addEventListener('keydown', handleKeydown)
  window.addEventListener('pointerdown', closeChapterSortOnOutside)
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleKeydown)
  window.removeEventListener('pointerdown', closeChapterSortOnOutside)
})

async function load() {
  loading.value = true
  try {
    const payload = await api.localLibrary()
    items.value = payload.items || []
    rootOrder.value = Array.isArray(payload.downloadDirs) ? payload.downloadDirs : []
    const availableRoots = new Set(rootOrder.value)
    if (activeRoot.value && !availableRoots.has(activeRoot.value)) activeRoot.value = ''
    const available = new Set(items.value.map(keyOf))
    selectedPaths.value = selectedPaths.value.filter((path) => available.has(path))
    if (!selectedPaths.value.length) selectionMode.value = false
  } catch (err) {
    notify(`本地库加载失败：${err.message}`, true)
  } finally {
    loading.value = false
  }
}

function keyOf(item) {
  return item.path || item.title
}

function isSelected(item) {
  return selectedPaths.value.includes(keyOf(item))
}

function enterSelection(item, index, event) {
  event?.preventDefault()
  const path = keyOf(item)
  selectionMode.value = true
  if (!selectedPaths.value.includes(path)) selectedPaths.value = [...selectedPaths.value, path]
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
    filtered.value.slice(start, end + 1).forEach((entry) => next.add(keyOf(entry)))
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
  } else {
    selectedPaths.value = [...selectedPaths.value, path]
  }
}

function selectAll() {
  selectedPaths.value = filtered.value.map(keyOf)
  selectionMode.value = selectedPaths.value.length > 0
  selectionAnchor.value = selectedPaths.value.length ? 0 : -1
}

function clearSelection() {
  selectionMode.value = false
  selectedPaths.value = []
  selectionAnchor.value = -1
}

function handleKeydown(event) {
  if (event.key !== 'Escape') return
  if (chapterSortOpen.value) {
    chapterSortOpen.value = false
    return
  }
  if (selectionMode.value) clearSelection()
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
  return item.cover ? api.localFileUrl(item.cover) : ''
}

function fileUrl(file, download = false) {
  return api.localFileUrl(file.path, download)
}

function chapterName(file) {
  return String(file.name || '').replace(/\.[^.]+$/, '')
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

const chapterSortLabel = computed(() => (chapterSortMode.value === 'time' ? '添加时间' : '标题'))

function toggleChapterSortMenu() {
  if (chapterSortOpen.value) {
    chapterSortOpen.value = false
    return
  }
  const trigger = chapterSortRoot.value?.getBoundingClientRect()
  const estimatedMenuHeight = 150
  chapterSortUpward.value = Boolean(
    trigger &&
      trigger.bottom + estimatedMenuHeight > window.innerHeight &&
      trigger.top > estimatedMenuHeight,
  )
  chapterSortOpen.value = true
}

function closeChapterSortOnOutside(event) {
  if (!chapterSortOpen.value || chapterSortRoot.value?.contains(event.target)) return
  chapterSortOpen.value = false
}

function chooseChapterSortMode(mode) {
  chapterSortMode.value = mode
  chapterSortOpen.value = false
}

function chooseChapterSortDirection(descending) {
  chapterSortDesc.value = descending
  chapterSortOpen.value = false
}

async function openDetail(item) {
  active.value = item
  heroTint.value = ''
  if (item.cover) extractTint(coverUrl(item))
  chapterQuery.value = ''
  chapterApplied.value = ''
  chapterSortMode.value = 'title'
  chapterSortDesc.value = false
  chapterSortOpen.value = false
  for (const file of item.files || []) {
    if ((file.ext !== 'cbz' && file.ext !== 'zip') || file.thumbnail) continue
    try {
      const payload = await api.localCBZ(file.path)
      const first = (payload.items || []).find((entry) => entry.name && !entry.name.endsWith('/'))
      if (first) file.thumbnail = api.localCBZFileUrl(file.path, first.name)
    } catch {
      file.thumbnail = ''
    }
  }
}

function closeDetail() {
  chapterSortOpen.value = false
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
  readerLoading.value = true
  try {
    if (file.ext === 'cbz' || file.ext === 'zip') {
      const payload = await api.localCBZ(file.path)
      reader.value = { file, images: payload.items || [], text: '' }
    } else {
      reader.value = { file, images: [], text: '' }
      const response = await fetch(api.localFileUrl(file.path))
      if (!response.ok) throw new Error(`无法读取文件（${response.status}）`)
      reader.value = { file, images: [], text: await response.text() }
    }
  } catch (err) {
    reader.value = null
    notify(`打开失败：${err.message}`, true)
  } finally {
    readerLoading.value = false
  }
}

function closeReader() {
  reader.value = null
}

function readerImageUrl(entry) {
  return api.localCBZFileUrl(reader.value.file.path, entry.name)
}

function downloadReaderFile() {
  if (!reader.value) return
  window.location.href = api.localFileUrl(reader.value.file.path, true)
}
</script>

<template>
  <div v-if="active" class="local-detail-view" :style="heroStyle">
    <div class="detail-hero local-detail-hero">
      <button class="detail-cover-back" type="button" aria-label="返回本地库" @click="closeDetail">
        <ArrowLeft :size="24" :stroke-width="1.7" />
      </button>
      <div class="detail-cover-wrap">
        <div class="detail-cover">
          <img v-if="active.cover" :src="coverUrl(active)" :alt="active.title" />
          <span v-else class="cover-fallback"><BookOpen :size="30" /></span>
        </div>
      </div>
      <div class="detail-info">
        <div class="detail-info-head">
          <h2>{{ active.title }}</h2>
        </div>
        <div class="meta detail-meta-line">
          <span class="badge" :class="active.kind === 'book' ? 'primary' : ''">
            {{ active.kind === 'book' ? '书籍' : '漫画' }}
          </span>
          <span v-if="active.author" class="author-pill">
            <UserRound :size="14" />
            {{ active.author }}
          </span>
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
        <div ref="chapterSortRoot" class="chapter-sort-wrap" aria-label="章节排序">
          <button
            class="chapter-sort-trigger"
            type="button"
            aria-haspopup="menu"
            :aria-expanded="chapterSortOpen"
            @click="toggleChapterSortMenu"
          >
            <span>{{ chapterSortLabel }}</span>
            <component :is="chapterSortDesc ? ArrowDown : ArrowUp" :size="14" :stroke-width="1.6" />
          </button>
          <Transition name="select-popup">
            <div v-if="chapterSortOpen" class="chapter-sort-menu" :class="chapterSortUpward ? 'above' : 'below'" role="menu">
              <div class="chapter-sort-group">
                <button
                  type="button"
                  role="menuitemradio"
                  :aria-checked="chapterSortMode === 'title'"
                  :class="{ active: chapterSortMode === 'title' }"
                  @click="chooseChapterSortMode('title')"
                >
                  <span>标题</span>
                  <Check v-if="chapterSortMode === 'title'" class="chapter-sort-check" :size="15" />
                </button>
                <button
                  type="button"
                  role="menuitemradio"
                  :aria-checked="chapterSortMode === 'time'"
                  :class="{ active: chapterSortMode === 'time' }"
                  @click="chooseChapterSortMode('time')"
                >
                  <span>添加时间</span>
                  <Check v-if="chapterSortMode === 'time'" class="chapter-sort-check" :size="15" />
                </button>
              </div>
              <div class="chapter-sort-group">
                <button
                  type="button"
                  role="menuitemradio"
                  :aria-checked="!chapterSortDesc"
                  :class="{ active: !chapterSortDesc }"
                  @click="chooseChapterSortDirection(false)"
                >
                  <ArrowUp :size="15" :stroke-width="1.6" />
                  <span>升序</span>
                  <Check v-if="!chapterSortDesc" class="chapter-sort-check" :size="15" />
                </button>
                <button
                  type="button"
                  role="menuitemradio"
                  :aria-checked="chapterSortDesc"
                  :class="{ active: chapterSortDesc }"
                  @click="chooseChapterSortDirection(true)"
                >
                  <ArrowDown :size="15" :stroke-width="1.6" />
                  <span>降序</span>
                  <Check v-if="chapterSortDesc" class="chapter-sort-check" :size="15" />
                </button>
              </div>
            </div>
          </Transition>
        </div>
      </div>

      <div v-if="!activeChapterFiles.length" class="empty">没有可阅读的章节</div>
      <div v-else class="local-chapter-poster-grid">
        <button
          v-for="file in activeChapterFiles"
          :key="file.path"
          class="local-chapter-poster"
          type="button"
          :title="file.name"
          @click="openReader(file)"
        >
          <span class="local-chapter-poster-cover">
            <img v-if="file.thumbnail" :src="file.thumbnail" :alt="file.name" loading="lazy" />
          </span>
          <span class="local-chapter-poster-name">{{ chapterName(file) }}</span>
        </button>
      </div>
    </section>
  </div>

  <div v-else>
    <div class="toolbar library-toolbar">
      <div class="segmented" role="tablist" aria-label="本地库类型">
        <button type="button" :class="{ active: kind === 'all' }" @click="kind = 'all'">
          <Layers3 :size="16" />
          全部
          <span class="segmented-count">{{ items.length }}</span>
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
      </div>
      <span class="spacer" />
      <label class="search-field local-search">
        <Search :size="16" />
        <input v-model="query" aria-label="筛选本地作品" placeholder="搜索作品或作者" />
      </label>
    </div>

    <div v-if="libraryRoots.length" class="local-root-strip" aria-label="本地库目录">
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

    <div v-if="loading && !items.length" class="empty">
      <Loader2 :size="22" class="spin" />
      <span>正在读取本地库</span>
    </div>
    <div v-else-if="!items.length" class="card empty">
      <FolderOpen :size="26" />
      <p>本地库还是空的。下载完成后，漫画 CBZ 与书籍文本会显示在这里。</p>
    </div>
    <div v-else-if="!filtered.length" class="card empty">
      <Search :size="26" />
      <p>没有匹配的作品。</p>
    </div>
    <div v-else class="local-library-grid">
      <button
        v-for="(item, index) in filtered"
        :key="keyOf(item)"
        class="card local-library-card"
        :class="{ selected: isSelected(item), selecting: selectionMode }"
        type="button"
        @click="handleCardClick(item, index, $event)"
        @contextmenu.prevent="enterSelection(item, index, $event)"
      >
        <span v-if="isSelected(item)" class="local-selection-check"><Check :size="16" /></span>
        <span class="local-library-cover">
          <img v-if="item.cover" :src="coverUrl(item)" :alt="item.title" loading="lazy" />
          <span v-else class="cover-fallback"><BookOpen :size="30" /></span>
        </span>
        <span class="local-library-body">
          <strong class="local-library-title">{{ item.title }}</strong>
          <span class="local-library-meta">
            <span>{{ item.author || '未知作者' }}</span>
            <span>{{ item.files?.length || item.totalFiles || 0 }}章</span>
          </span>
        </span>
      </button>
    </div>
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
          <p v-if="readerImages.length" class="muted small">
            共 {{ readerImages.length }} 张 · 条漫模式
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
        <div v-if="readerImages.length" class="reader-view">
          <img
            v-for="(entry, index) in readerImages"
            :key="entry.name"
            :src="readerImageUrl(entry)"
            :alt="`第 ${index + 1} 张`"
            loading="lazy"
          />
        </div>
        <pre v-else class="reader-text">{{ readerText || '（空文件）' }}</pre>
      </template>
    </div>
  </div>
</template>
