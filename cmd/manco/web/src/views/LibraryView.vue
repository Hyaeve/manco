<script setup>
import { computed, onMounted, ref } from 'vue'
import { BookOpen, ChevronDown, ChevronRight, FolderOpen, HardDrive, Loader2, RefreshCw, Search } from 'lucide-vue-next'
import { api } from '../api'

const items = ref([])
const downloadDir = ref('')
const loading = ref(true)
const error = ref('')
const query = ref('')
const expanded = ref(new Set())

const visible = computed(() => {
  const keyword = query.value.trim().toLowerCase()
  if (!keyword) return items.value
  return items.value.filter((item) => item.title.toLowerCase().includes(keyword))
})

const totalChapters = computed(() => items.value.reduce((sum, item) => sum + item.chapters.length, 0))

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const payload = await api.library()
    items.value = payload.items || []
    downloadDir.value = payload.downloadDir || ''
    if (!expanded.value.size) {
      expanded.value = new Set(items.value.slice(0, 1).map((item) => keyOf(item)))
    }
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

function keyOf(item) {
  return `${item.sourceId}/${item.comicId}`
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

function cover(item) {
  return api.imageUrl(item.cover, item.sourceId)
}

function chapterOrder(chapters) {
  return [...chapters].sort((a, b) => (a.order || 0) - (b.order || 0))
}
</script>

<template>
  <div v-if="error" class="alert error">{{ error }}</div>

  <div class="toolbar">
    <label class="field search-input">
      <span>筛选作品</span>
      <input v-model="query" class="input" placeholder="输入作品名" />
    </label>
    <div class="field">
      <span>&nbsp;</span>
      <button class="btn secondary" type="button" :disabled="loading" @click="load">
        <RefreshCw :size="15" :class="{ spin: loading }" />
        刷新
      </button>
    </div>
  </div>

  <div class="alert info inline" v-if="downloadDir">
    <HardDrive :size="16" />
    <span>本地目录：{{ downloadDir }}</span>
    <span class="spacer" />
    <span class="muted small">共 {{ items.length }} 部作品 / {{ totalChapters }} 个 CBZ</span>
  </div>

  <div v-if="loading && !items.length" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载本地资料库</span>
  </div>
  <div v-else-if="!items.length" class="card empty">
    <FolderOpen :size="26" />
    <p>资料库还是空的。完成一话下载后，对应的 CBZ 会显示在这里。</p>
  </div>
  <div v-else-if="!visible.length" class="card empty">
    <Search :size="26" />
    <p>没有匹配「{{ query }}」的作品。</p>
  </div>
  <div v-else class="section">
    <div v-for="item in visible" :key="keyOf(item)" class="card" style="margin-bottom: 12px">
      <button class="library-head" type="button" @click="toggle(item)">
        <img
          v-if="item.cover"
          :src="cover(item)"
          alt=""
          style="width: 42px; height: 58px; object-fit: cover; border-radius: 4px"
        />
        <span v-else class="cover-fallback" style="width: 42px; height: 58px; position: static">
          <BookOpen :size="18" />
        </span>
        <span class="library-info">
          <span class="comic-title">{{ item.title }}</span>
          <span class="muted small">
            {{ item.sourceId }} · {{ item.chapters.length }} 话 · 每话一个 CBZ
          </span>
        </span>
        <component :is="isOpen(item) ? ChevronDown : ChevronRight" :size="18" />
      </button>
      <div v-if="isOpen(item)" class="chapter-list" style="border: none; border-top: 1px solid var(--border); border-radius: 0">
        <div v-for="chapter in chapterOrder(item.chapters)" :key="chapter.id" class="chapter-row">
          <span class="badge">{{ chapter.pages || '?' }} 页</span>
          <span class="title">{{ chapter.title }}</span>
          <span class="muted small" style="max-width: 42%; word-break: break-all; text-align: right">
            {{ chapter.filePath }}
          </span>
        </div>
      </div>
    </div>
  </div>
</template>
