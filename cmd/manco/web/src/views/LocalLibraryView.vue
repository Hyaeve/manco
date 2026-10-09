<script setup>
import { computed, onMounted, ref } from 'vue'
import { BookOpen, ChevronDown, ChevronRight, Download, FolderOpen, HardDrive, Loader2, RefreshCw, Search } from 'lucide-vue-next'
import { api } from '../api'

const items = ref([])
const downloadDir = ref('')
const loading = ref(true)
const error = ref('')
const query = ref('')
const kind = ref('all')
const expanded = ref(new Set())

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
const totalSize = computed(() => items.value.reduce((sum, item) => sum + (item.size || 0), 0))

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const payload = await api.localLibrary()
    items.value = payload.items || []
    downloadDir.value = payload.downloadDir || ''
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

function keyOf(item) {
  return item.path || item.title
}

function isOpen(item) {
  return expanded.value.has(keyOf(item))
}

function toggle(item) {
  const next = new Set(expanded.value)
  if (next.has(keyOf(item))) {
    next.delete(keyOf(item))
  } else {
    next.add(keyOf(item))
  }
  expanded.value = next
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
</script>

<template>
  <div v-if="error" class="alert error">{{ error }}</div>

  <div class="toolbar">
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
    <label class="field search-input">
      <span>筛选作品</span>
      <input v-model="query" class="input" placeholder="输入作品名或作者" />
    </label>
    <div class="field">
      <span>&nbsp;</span>
      <button class="btn secondary" type="button" :disabled="loading" @click="load">
        <RefreshCw :size="15" :class="{ spin: loading }" />
        刷新
      </button>
    </div>
  </div>

  <div v-if="downloadDir" class="alert info inline">
    <HardDrive :size="16" />
    <span>本地目录：{{ downloadDir }}</span>
    <span class="spacer" />
    <span class="muted small">共 {{ items.length }} 部作品 / {{ fileSize(totalSize) }}</span>
  </div>

  <div v-if="loading && !items.length" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>读取 downloads 目录</span>
  </div>
  <div v-else-if="!items.length" class="card empty">
    <FolderOpen :size="26" />
    <p>下载目录还是空的。下载完成后，漫画 CBZ 与书籍文本会显示在这里。</p>
  </div>
  <div v-else-if="!filtered.length" class="card empty">
    <Search :size="26" />
    <p>没有匹配的作品。</p>
  </div>
  <div v-else class="section">
    <div v-for="item in filtered" :key="keyOf(item)" class="card" style="margin-bottom: 12px">
      <button class="library-head" type="button" @click="toggle(item)">
        <img
          v-if="item.cover"
          :src="coverUrl(item)"
          alt=""
          style="width: 42px; height: 58px; object-fit: cover; border-radius: 4px"
        />
        <span v-else class="cover-fallback" style="width: 42px; height: 58px; position: static">
          <BookOpen :size="18" />
        </span>
        <span class="library-info">
          <span class="comic-title">{{ item.title }}</span>
          <span class="muted small">
            {{ item.kind === 'book' ? '书籍' : '漫画' }}
            <template v-if="item.author"> · {{ item.author }}</template>
            · {{ item.files.length }} 个文件 · {{ fileSize(item.size) }}
          </span>
          <span v-if="item.description" class="muted small" style="overflow: hidden; text-overflow: ellipsis">
            {{ item.description }}
          </span>
        </span>
        <component :is="isOpen(item) ? ChevronDown : ChevronRight" :size="18" />
      </button>
      <div v-if="isOpen(item)" class="chapter-list" style="border: none; border-top: 1px solid var(--border); border-radius: 0">
        <div v-for="file in item.files" :key="file.path" class="chapter-row">
          <span class="badge">{{ file.ext.toUpperCase() }}</span>
          <span class="title">{{ file.name }}</span>
          <span class="muted small">{{ fileSize(file.size) }}</span>
          <a class="btn ghost icon" :href="fileUrl(file)" target="_blank" rel="noreferrer" :title="`查看 ${file.name}`">
            <BookOpen :size="15" />
          </a>
          <a class="btn ghost icon" :href="fileUrl(file, true)" :title="`下载 ${file.name}`">
            <Download :size="15" />
          </a>
        </div>
      </div>
    </div>
  </div>
</template>
