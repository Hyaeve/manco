<script setup>
import { computed, onMounted, ref } from 'vue'
import {
  BookOpen,
  ChevronLeft,
  ChevronRight,
  Download,
  FileText,
  FolderOpen,
  Loader2,
  RefreshCw,
  Search,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import { notify } from '../stores/notices'

const items = ref([])
const loading = ref(true)
const query = ref('')
const kind = ref('all')
const active = ref(null)
const reader = ref(null)
const readerLoading = ref(false)
const pageIndex = ref(0)

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

onMounted(load)

async function load() {
  loading.value = true
  try {
    const payload = await api.localLibrary()
    items.value = payload.items || []
  } catch (err) {
    notify(`本地库加载失败：${err.message}`, true)
  } finally {
    loading.value = false
  }
}

function keyOf(item) {
  return item.path || item.title
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

function openDetail(item) {
  active.value = item
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
          <h3>文件</h3>
        </div>
        <div class="chapter-list local-file-list">
          <div v-for="file in activeFiles" :key="file.path" class="chapter-row">
            <FileText v-if="file.ext === 'txt'" :size="16" />
            <BookOpen v-else :size="16" />
            <span class="title">{{ file.name }}</span>
            <span class="muted small">{{ fileSize(file.size) }}</span>
            <button class="btn ghost icon" type="button" :title="`阅读 ${file.name}`" @click="openReader(file)">
              <BookOpen :size="15" />
            </button>
            <a class="btn ghost icon" :href="fileUrl(file, true)" :title="`下载 ${file.name}`">
              <Download :size="15" />
            </a>
          </div>
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
        v-for="item in filtered"
        :key="keyOf(item)"
        class="card local-library-card"
        type="button"
        @click="openDetail(item)"
      >
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
