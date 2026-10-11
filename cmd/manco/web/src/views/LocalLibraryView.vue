<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  Check,
  BookOpen,
  ChevronLeft,
  ChevronRight,
  Download,
  FileText,
  FolderOpen,
  Languages,
  ListChecks,
  Loader2,
  Merge,
  RefreshCw,
  Search,
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
const pageIndex = ref(0)
const selectionMode = ref(false)
const selectedPaths = ref([])
const selectionAnchor = ref(-1)
const batchWorking = ref(false)

const filtered = computed(() => {
  const keyword = query.value.trim().toLowerCase()
  return items.value.filter((item) => {
    if (kind.value !== 'all' && item.kind !== kind.value) return false
    if (!keyword) return true
    return (
      (item.title || '').toLowerCase().includes(keyword) ||
      (item.author || '').toLowerCase().includes(keyword)
    )
  })
})

const comicCount = computed(() => items.value.filter((item) => item.kind !== 'book').length)
const bookCount = computed(() => items.value.filter((item) => item.kind === 'book').length)

const activeFiles = computed(() => active.value?.files || [])
const readerTitle = computed(() => reader.value?.file?.name || '')
const readerImages = computed(() => reader.value?.images || [])
const readerText = computed(() => reader.value?.text || '')
const readerPage = computed(() => readerImages.value[pageIndex.value] || null)
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
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleKeydown)
})

async function load() {
  loading.value = true
  try {
    const payload = await api.localLibrary()
    items.value = payload.items || []
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

function coverUrl(item) {
  return item.cover ? api.localFileUrl(item.cover) : ''
}

function fileUrl(file, download = false) {
  return api.localFileUrl(file.path, download)
}

function fileSize(bytes) {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)))
  return `${(bytes / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

async function openDetail(item) {
  active.value = item
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
  active.value = null
}

async function openReader(file) {
  readerLoading.value = true
  pageIndex.value = 0
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
  pageIndex.value = 0
}

function nextPage() {
  if (pageIndex.value < readerImages.value.length - 1) pageIndex.value += 1
}

function prevPage() {
  if (pageIndex.value > 0) pageIndex.value -= 1
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
  <div v-if="active" class="local-detail-view">
    <div class="toolbar">
      <button class="btn secondary small" type="button" @click="closeDetail">
        <ChevronLeft :size="15" />
        返回本地库
      </button>
      <span class="spacer" />
      <span class="muted small">{{ activeFiles.length }} 个文件 · {{ fileSize(active.size) }}</span>
    </div>

    <div class="local-detail">
      <div class="local-detail-cover">
        <img v-if="active.cover" :src="coverUrl(active)" :alt="active.title" />
        <span v-else class="cover-fallback"><BookOpen :size="34" /></span>
      </div>
      <div class="local-detail-info">
        <div class="inline">
          <span class="badge" :class="active.kind === 'book' ? 'primary' : ''">
            {{ active.kind === 'book' ? '书籍' : '漫画' }}
          </span>
        </div>
        <h2>{{ active.title }}</h2>
        <p class="muted">{{ active.author || '未知作者' }}</p>
        <p v-if="active.description" class="local-detail-desc">{{ active.description }}</p>
        <div class="section-head local-file-head">
          <h3>章节</h3>
        </div>
        <div class="local-chapter-grid">
          <article v-for="file in activeFiles" :key="file.path" class="local-chapter-card">
            <button class="local-chapter-cover" type="button" @click="openReader(file)">
              <img v-if="file.thumbnail" :src="file.thumbnail" :alt="file.name" loading="lazy" />
              <BookOpen v-else :size="26" />
            </button>
            <div class="local-chapter-copy">
              <strong :title="file.name">{{ file.name }}</strong>
              <span>{{ file.ext === 'txt' ? '文本' : '漫画章节' }} · {{ fileSize(file.size) }}</span>
            </div>
            <div class="local-chapter-actions">
              <button class="btn ghost icon" type="button" :aria-label="`阅读 ${file.name}`" @click="openReader(file)">
                <BookOpen :size="15" />
              </button>
              <a class="btn ghost icon" :href="fileUrl(file, true)" :aria-label="`下载 ${file.name}`">
                <Download :size="15" />
              </a>
            </div>
          </article>
          <div v-if="!activeFiles.length" class="empty">没有可阅读的文件</div>
        </div>
      </div>
    </div>
  </div>

  <div v-else>
    <div class="toolbar library-toolbar">
      <div class="segmented" role="tablist" aria-label="本地库类型">
        <button type="button" :class="{ active: kind === 'all' }" @click="kind = 'all'">
          全部
          <span class="segmented-count">{{ items.length }}</span>
        </button>
        <button type="button" :class="{ active: kind === 'comic' }" @click="kind = 'comic'">
          漫画
          <span class="segmented-count">{{ comicCount }}</span>
        </button>
        <button type="button" :class="{ active: kind === 'book' }" @click="kind = 'book'">
          书籍
          <span class="segmented-count">{{ bookCount }}</span>
        </button>
      </div>
      <label class="search-field local-search">
        <Search :size="16" />
        <input v-model="query" aria-label="筛选本地作品" placeholder="搜索作品或作者" />
      </label>
      <button class="btn secondary small" type="button" :disabled="loading" @click="load">
        <RefreshCw :size="15" :class="{ spin: loading }" />
        刷新
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
          <strong>{{ item.title }}</strong>
          <span>{{ item.author || '未知作者' }}</span>
          <span>{{ item.files.length }} 个文件 · {{ fileSize(item.size) }}</span>
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
    </div>
  </div>

  <div v-if="reader || readerLoading" class="modal-backdrop reader-backdrop" @click.self="closeReader">
    <div class="modal reader-modal">
      <div class="modal-head">
        <div>
          <h2>{{ readerTitle }}</h2>
          <p v-if="readerImages.length" class="muted small">
            第 {{ pageIndex + 1 }} / {{ readerImages.length }} 页
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
          <img v-if="readerPage" :src="readerImageUrl(readerPage)" :alt="`第 ${pageIndex + 1} 页`" />
        </div>
        <pre v-else class="reader-text">{{ readerText || '（空文件）' }}</pre>
        <div v-if="readerImages.length" class="reader-nav">
          <button class="btn secondary small" type="button" :disabled="pageIndex <= 0" @click="prevPage">
            <ChevronLeft :size="15" />
            上一页
          </button>
          <span class="muted small">{{ pageIndex + 1 }} / {{ readerImages.length }}</span>
          <button class="btn secondary small" type="button" :disabled="pageIndex >= readerImages.length - 1" @click="nextPage">
            下一页
            <ChevronRight :size="15" />
          </button>
        </div>
      </template>
    </div>
  </div>
</template>
