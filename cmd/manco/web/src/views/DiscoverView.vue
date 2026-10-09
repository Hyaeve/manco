<script setup>
import { nextTick, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { BookOpen, ChevronLeft, ChevronRight, ImageOff, Loader2, Search } from 'lucide-vue-next'
import { api } from '../api'

const route = useRoute()
const columns = ref([])
const columnElements = new Map()

function sourceFilters(source) {
  const values = {}
  for (const group of source.filters || []) {
    values[group.key] = group.default ?? group.options?.[0]?.value ?? ''
  }
  return values
}

function setColumnElement(id, element) {
  if (element) {
    columnElements.set(id, element)
    return
  }
  columnElements.delete(id)
}

onMounted(async () => {
  try {
    const payload = await api.sources()
    columns.value = (payload.items || []).map((source) => ({
      source,
      query: '',
      mode: 'browse',
      page: 1,
      items: [],
      hasMore: false,
      loading: false,
      error: '',
      filters: sourceFilters(source),
    }))
    await Promise.all(columns.value.map((column) => load(column)))
    await focusRequestedSource()
  } catch (err) {
    columns.value = [
      {
        source: { id: 'error', name: '漫画源加载失败', canSearch: false, filters: [] },
        query: '',
        mode: 'browse',
        page: 1,
        items: [],
        hasMore: false,
        loading: false,
        error: err.message,
        filters: {},
      },
    ]
  }
})

watch(
  () => route.query.source,
  async () => {
    await focusRequestedSource()
  },
)

async function focusRequestedSource() {
  const sourceId = typeof route.query.source === 'string' ? route.query.source : ''
  if (!sourceId) return
  await nextTick()
  columnElements.get(sourceId)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

async function load(column) {
  if (!column.source.id || column.source.id === 'error') return
  column.loading = true
  column.error = ''
  try {
    const result =
      column.mode === 'search' && column.query.trim()
        ? await api.search(column.source.id, column.query.trim(), column.page)
        : await api.browse(column.source.id, column.filters, column.page)
    column.items = result.items || []
    column.hasMore = Boolean(result.hasMore)
  } catch (err) {
    column.items = []
    column.hasMore = false
    column.error = err.message
  } finally {
    column.loading = false
  }
}

async function submitSearch(column) {
  column.page = 1
  column.mode = column.query.trim() ? 'search' : 'browse'
  await load(column)
}

async function changeFilter(column) {
  column.query = ''
  column.mode = 'browse'
  column.page = 1
  await load(column)
}

async function changePage(column, delta) {
  const next = column.page + delta
  if (next < 1) return
  column.page = next
  await load(column)
}

function cover(item) {
  return api.imageUrl(item.cover, item.sourceId)
}
</script>

<template>
  <div class="discover-board">
    <section
      v-for="column in columns"
      :key="column.source.id"
      :ref="(element) => setColumnElement(column.source.id, element)"
      class="source-column"
    >
      <header class="source-column-head">
        <div>
          <h2>{{ column.source.name }}</h2>
          <p>{{ column.source.description }}</p>
        </div>
        <span class="badge primary">{{ column.items.length }} 项</span>
      </header>

      <div class="source-column-tools">
        <div class="source-search">
          <input
            v-model="column.query"
            class="input"
            :disabled="!column.source.canSearch || column.loading"
            placeholder="搜索作品"
            @keyup.enter="submitSearch(column)"
          />
          <button
            class="btn small"
            type="button"
            :disabled="!column.source.canSearch || column.loading"
            :aria-label="`搜索 ${column.source.name}`"
            @click="submitSearch(column)"
          >
            <Loader2 v-if="column.loading" :size="15" class="spin" />
            <Search v-else :size="15" />
          </button>
        </div>

        <div v-if="column.source.filters?.length" class="source-filter-grid">
          <label v-for="group in column.source.filters" :key="group.key" class="field compact">
            <span>{{ group.label }}</span>
            <select
              v-model="column.filters[group.key]"
              class="select"
              :disabled="column.loading"
              @change="changeFilter(column)"
            >
              <option v-for="option in group.options" :key="`${group.key}-${option.value}`" :value="option.value">
                {{ option.label }}
              </option>
            </select>
          </label>
        </div>
      </div>

      <div v-if="column.error" class="alert error">{{ column.error }}</div>

      <div v-if="column.loading && !column.items.length" class="source-loading">
        <Loader2 :size="22" class="spin" />
        <span>正在加载</span>
      </div>

      <div v-else-if="!column.items.length" class="source-empty">
        <ImageOff :size="24" />
        <span>没有作品</span>
      </div>

      <div v-else class="source-comic-grid">
        <RouterLink
          v-for="item in column.items"
          :key="`${item.sourceId}-${item.id}`"
          class="comic-card source-comic-card"
          :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.id } }"
        >
          <div class="comic-cover">
            <img v-if="item.cover" :src="cover(item)" :alt="item.title" loading="lazy" />
            <span v-else class="cover-fallback"><BookOpen :size="26" /></span>
          </div>
          <div class="comic-body">
            <span class="comic-title">{{ item.title }}</span>
            <span class="comic-meta">{{ item.author || '未知作者' }}</span>
          </div>
        </RouterLink>
      </div>

      <div class="source-pagination">
        <button
          class="btn secondary small"
          type="button"
          :disabled="column.page <= 1 || column.loading"
          @click="changePage(column, -1)"
        >
          <ChevronLeft :size="15" />
          上一页
        </button>
        <span class="muted small">第 {{ column.page }} 页</span>
        <button
          class="btn secondary small"
          type="button"
          :disabled="!column.hasMore || column.loading"
          @click="changePage(column, 1)"
        >
          下一页
          <ChevronRight :size="15" />
        </button>
      </div>
    </section>
  </div>
</template>
