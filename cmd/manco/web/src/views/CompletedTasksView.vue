<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, ChevronDown, ChevronRight, CircleCheck, Loader2, RefreshCw, Search, Trash2, X } from 'lucide-vue-next'
import { api } from '../api'
import ContentLink from '../components/ContentLink.vue'
import { askConfirm } from '../stores/confirm'

const route = useRoute()
const router = useRouter()
const items = ref([])
const loading = ref(true)
const error = ref('')
const busy = ref(0)
const expandedGroups = ref(new Set())

function queryText(value) {
  return typeof value === 'string' ? value : ''
}

const keyword = ref(queryText(route.query.q).trim())
const searchInput = ref(keyword.value)

const completedItems = computed(() => items.value.filter((item) => item.status === 'completed'))

const visibleItems = computed(() => {
  const key = keyword.value.trim().toLowerCase()
  if (!key) return completedItems.value
  return completedItems.value.filter((item) => {
    const haystack = `${item.comicTitle || ''} ${item.chapterTitle || ''} ${item.comicAuthor || ''}`.toLowerCase()
    return haystack.includes(key)
  })
})

const groups = computed(() => {
  const map = new Map()
  for (const item of visibleItems.value) {
    const key = `${item.sourceId || ''}:${item.comicId || item.comicTitle || item.id}`
    let group = map.get(key)
    if (!group) {
      group = {
        key,
        title: item.comicTitle || '未命名作品',
        author: item.comicAuthor || '未知作者',
        items: [],
      }
      map.set(key, group)
    }
    group.items.push(item)
  }
  return [...map.values()].map((group) => {
    group.items.sort((left, right) => {
      const leftTime = new Date(left.finishedAt || left.updatedAt || 0).getTime()
      const rightTime = new Date(right.finishedAt || right.updatedAt || 0).getTime()
      return rightTime - leftTime
    })
    return group
  })
})

watch(
  () => route.query.q,
  (value) => {
    const next = queryText(value).trim()
    if (keyword.value !== next) keyword.value = next
    if (searchInput.value !== next) searchInput.value = next
  },
)

onMounted(async () => {
  await load()
})

async function load(silent = false) {
  if (!silent) loading.value = true
  try {
    const payload = await api.downloads()
    items.value = payload.items || []
    if (silent) error.value = ''
  } catch (err) {
    if (!silent) error.value = err.message
  } finally {
    loading.value = false
  }
}

function isGroupExpanded(key) {
  return expandedGroups.value.has(key)
}

function toggleGroup(key) {
  const next = new Set(expandedGroups.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expandedGroups.value = next
}

function applyKeyword() {
  keyword.value = searchInput.value.trim()
  const query = { ...route.query }
  if (keyword.value) query.q = keyword.value
  else delete query.q
  router.replace({ name: 'completed', query })
}

function clearKeyword() {
  searchInput.value = ''
  applyKeyword()
}

function formatTime(item) {
  const raw = item.finishedAt || item.updatedAt
  if (!raw) return '—'
  return new Date(raw).toLocaleString()
}

async function remove(item) {
  const confirmed = await askConfirm({
    title: '删除已完成记录',
    message: `确定删除「${item.comicTitle} ${item.chapterTitle}」的已完成记录吗？CBZ 文件会保留。`,
    confirmText: '删除',
    danger: true,
  })
  if (!confirmed) return
  busy.value = item.id
  error.value = ''
  try {
    await api.deleteDownload(item.id, false)
    items.value = items.value.filter((row) => row.id !== item.id)
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = 0
  }
}
</script>

<template>
  <div class="page-panel completed-page">
    <div v-if="error" class="alert error">{{ error }}</div>

    <div class="toolbar completed-toolbar">
      <div class="inline">
        <ContentLink class="btn ghost icon" to="/downloads" aria-label="返回任务列表">
          <ArrowLeft :size="17" />
        </ContentLink>
        <strong>已完成</strong>
        <span class="badge success">{{ completedItems.length }}</span>
      </div>
      <span class="spacer" />
      <button class="btn secondary small" type="button" :disabled="loading" @click="load()">
        <RefreshCw :size="15" :class="{ spin: loading }" />
        刷新
      </button>
      <label class="task-search">
        <Search :size="15" />
        <input
          v-model="searchInput"
          class="task-search-input"
          type="search"
          placeholder="搜索作品或章节"
          @keyup.enter="applyKeyword"
          @search="applyKeyword"
        />
        <button v-if="searchInput" class="icon-btn" type="button" aria-label="清除搜索" @click="clearKeyword">
          <X :size="14" />
        </button>
      </label>
    </div>

    <div v-if="loading && !items.length" class="empty">
      <Loader2 :size="22" class="spin" />
      <span>加载已完成记录</span>
    </div>
    <div v-else-if="!completedItems.length" class="card empty">
      <CircleCheck :size="26" />
      <p>还没有已完成的章节。</p>
      <ContentLink class="btn small" to="/downloads">查看任务列表</ContentLink>
    </div>
    <div v-else-if="!groups.length" class="card empty">
      <Search :size="26" />
      <p>没有匹配“{{ keyword }}”的已完成记录。</p>
    </div>
    <div v-else class="completed-task-groups">
      <article v-for="group in groups" :key="group.key" class="completed-task-group">
        <button
          class="completed-task-head"
          type="button"
          :aria-expanded="isGroupExpanded(group.key)"
          @click="toggleGroup(group.key)"
        >
          <span class="completed-task-title">
            <strong>{{ group.title }}</strong>
            <small>{{ group.author }}</small>
          </span>
          <span class="completed-task-summary">
            <span class="badge success">已完成 {{ group.items.length }} 章</span>
          </span>
          <ChevronDown v-if="isGroupExpanded(group.key)" :size="18" />
          <ChevronRight v-else :size="18" />
        </button>
        <div v-if="isGroupExpanded(group.key)" class="completed-task-chapters">
          <div v-for="item in group.items" :key="item.id" class="completed-task-chapter">
            <span class="completed-chapter-title" :title="item.chapterTitle">{{ item.chapterTitle }}</span>
            <span class="completed-chapter-time">{{ formatTime(item) }}</span>
            <button class="icon-btn danger" type="button" :disabled="busy === item.id" aria-label="删除已完成记录" @click="remove(item)">
              <Trash2 :size="15" />
            </button>
          </div>
        </div>
      </article>
    </div>
  </div>
</template>
