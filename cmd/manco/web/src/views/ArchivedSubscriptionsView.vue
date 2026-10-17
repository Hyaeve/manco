<script setup>
import { computed, onDeactivated, onMounted, onUnmounted, ref } from 'vue'
import { Archive, ArrowLeft, BookOpen, Loader2, RefreshCw, RotateCcw, X } from 'lucide-vue-next'
import { api } from '../api'
import ContentLink from '../components/ContentLink.vue'
import SourceIcon from '../components/SourceIcon.vue'
import { askConfirm } from '../stores/confirm'
import { notify } from '../stores/notices'

const items = ref([])
const sources = ref({})
const jobStatsData = ref({})
const loading = ref(true)
const busy = ref(0)
const batchBusy = ref(false)
const selectionMode = ref(false)
const selected = ref(new Set())
let selectAnchor = -1

onMounted(async () => {
  await load()
  window.addEventListener('keydown', onKeydown)
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
})

onDeactivated(clearSelection)

function onKeydown(event) {
  if (event.key === 'Escape' && selectionMode.value) clearSelection()
}

const selectedItems = computed(() => items.value.filter((item) => selected.value.has(item.id)))
const selectedCount = computed(() => selectedItems.value.length)
const allSelected = computed(() => items.value.length > 0 && items.value.every((item) => selected.value.has(item.id)))

function isSelected(item) {
  return selected.value.has(item.id)
}

function enterSelection(item, index, event) {
  event?.preventDefault?.()
  const next = new Set(selected.value)
  if (event?.shiftKey && selectAnchor >= 0) {
    const from = Math.min(selectAnchor, index)
    const to = Math.max(selectAnchor, index)
    items.value.slice(from, to + 1).forEach((row) => next.add(row.id))
  } else {
    next.add(item.id)
  }
  selected.value = next
  selectionMode.value = true
  selectAnchor = index
}

function handleCardClick(item, index, event) {
  if (event?.shiftKey) event.preventDefault()
  const next = new Set(selected.value)
  if (event?.shiftKey && selectAnchor >= 0) {
    const from = Math.min(selectAnchor, index)
    const to = Math.max(selectAnchor, index)
    items.value.slice(from, to + 1).forEach((row) => next.add(row.id))
  } else if (next.has(item.id)) {
    next.delete(item.id)
  } else {
    next.add(item.id)
  }
  selected.value = next
  selectionMode.value = next.size > 0
  selectAnchor = index
}

// 选中态下拦截卡片内部链接的点击，改为切换选中。
function captureCardClick(item, index, event) {
  if (!selectionMode.value) return
  event.preventDefault()
  event.stopPropagation()
  handleCardClick(item, index, event)
}

function selectAll() {
  selected.value = new Set(items.value.map((item) => item.id))
  selectionMode.value = selected.value.size > 0
  selectAnchor = selected.value.size ? 0 : -1
}

function clearSelection() {
  selected.value = new Set()
  selectionMode.value = false
  selectAnchor = -1
}

async function load() {
  loading.value = true
  try {
    const [archived, sourcePayload] = await Promise.all([
      api.archivedSubscriptions(),
      api.sources().catch(() => ({ items: [] })),
    ])
    items.value = archived.items || []
    jobStatsData.value = archived.jobStats || {}
    for (const source of sourcePayload.items || []) {
      sources.value = { ...sources.value, [source.id]: source }
    }
    const available = new Set(items.value.map((item) => item.id))
    if ([...selected.value].some((id) => !available.has(id))) {
      selected.value = new Set([...selected.value].filter((id) => available.has(id)))
      if (!selected.value.size) selectionMode.value = false
    }
  } catch (err) {
    notify(`归档集加载失败：${err.message}`, 'error')
  } finally {
    loading.value = false
  }
}

function itemKind(item) {
  const kind = sources.value[item.sourceId]?.kind
  return kind === 'book' || kind === 'other' ? kind : 'comic'
}

function cover(item) {
  if (item.localCover) return api.localFileUrl(item.localCover)
  if (item.cachedCover) return item.cachedCover
  return api.imageUrl(item.cover, item.sourceId)
}

function formatArchivedAt(item) {
  const raw = item?.archivedAt
  if (!raw) return '未知时间'
  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) return '未知时间'
  return date.toLocaleString()
}

function archiveComicLink(item) {
  return {
    name: 'comic',
    params: { sourceId: item.sourceId, comicId: item.comicId },
    query: { archive: '1' },
  }
}

function completedTaskLink(item) {
  return {
    name: 'completed',
    query: { q: item.title || '' },
  }
}

function chapterCount(item) {
  return Number(item.chapterCount || 0)
}

async function restore(item) {
  const confirmed = await askConfirm({
    title: '移出归档',
    message: `确定把《${item.title}》移出归档并恢复到订阅清单吗？恢复后会重新启用该订阅。`,
    confirmText: '移出归档',
  })
  if (!confirmed) return
  busy.value = item.id
  try {
    await api.restoreSubscription(item.id)
    items.value = items.value.filter((row) => row.id !== item.id)
    notify(`《${item.title}》已移出归档，并恢复到订阅清单`, 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = 0
  }
}

async function batchRestore() {
  const rows = selectedItems.value.slice()
  if (!rows.length || batchBusy.value) return
  const confirmed = await askConfirm({
    title: '移出归档集',
    message: `确定把所选 ${rows.length} 个订阅移出归档并恢复到订阅清单吗？恢复后会重新启用。`,
    confirmText: '移出归档',
  })
  if (!confirmed) return
  batchBusy.value = true
  let done = 0
  let failed = 0
  try {
    for (const item of rows) {
      try {
        await api.restoreSubscription(item.id)
        done += 1
      } catch {
        failed += 1
      }
    }
  } finally {
    batchBusy.value = false
  }
  notify(failed ? `移出归档完成：成功 ${done} 项，失败 ${failed} 项` : `已将 ${done} 个订阅移出归档`, failed ? 'warning' : 'success')
  if (done > 0) clearSelection()
  await load()
}

function jobStats(item) {
  const aggregated = jobStatsData.value[`${item.sourceId}\u0000${item.comicId}`]
  if (aggregated) {
    return {
      success: Number(aggregated.succeeded || 0),
      failed: Number(aggregated.failed || 0),
    }
  }
  return {
    success: 0,
    failed: 0,
  }
}
</script>

<template>
  <div class="page-panel archive-page">
    <div class="toolbar archive-toolbar">
      <div class="inline">
    <ContentLink class="btn ghost icon" to="/subscriptions" aria-label="返回订阅清单">
          <ArrowLeft :size="17" />
    </ContentLink>
        <strong>归档集</strong>
        <span class="badge">{{ items.length }}</span>
      </div>
      <span class="spacer" />
      <button class="btn secondary small" type="button" :disabled="loading" @click="load">
        <RefreshCw :size="15" :class="{ spin: loading }" />
        刷新
      </button>
    </div>

    <div v-if="loading && !items.length" class="empty">
      <Loader2 :size="22" class="spin" />
      <span>加载归档集</span>
    </div>
    <div v-else-if="!items.length" class="card empty">
      <Archive :size="26" />
      <p>归档集为空。订阅卡片执行「归档」后会出现在这里。</p>
    </div>
    <div v-else class="archive-grid archive-page-grid">
      <article
        v-for="(item, index) in items"
        :key="item.id"
        class="archive-card"
        :class="{ selected: isSelected(item), selecting: selectionMode }"
        @contextmenu.prevent="enterSelection(item, index, $event)"
        @click.capture="captureCardClick(item, index, $event)"
      >
        <ContentLink class="archive-cover" :to="archiveComicLink(item)" :title="item.title">
          <img v-if="cover(item)" :src="cover(item)" :alt="item.title" loading="lazy" decoding="async" />
          <BookOpen v-else :size="22" />
        </ContentLink>
        <div class="archive-meta">
          <ContentLink class="archive-title" :to="archiveComicLink(item)" :title="item.title">
            {{ item.title }}
          </ContentLink>
          <span class="archive-sub">{{ item.author || '未知作者' }}</span>
          <span class="archive-sub">
            <template v-if="chapterCount(item) > 0">共 {{ chapterCount(item) }} 话 · </template>最新章节：{{ item.lastChapterTitle || '尚未记录' }}
          </span>
          <span class="archive-sub">归档：{{ formatArchivedAt(item) }}</span>
        </div>
        <div class="archive-stats">
          <span class="subscription-source archive-stat-source">
            <SourceIcon :src="sources[item.sourceId]?.icon || ''" :kind="itemKind(item)" :alt="item.sourceId" :size="18" />
          </span>
          <ContentLink class="subscription-stat success" :to="completedTaskLink(item)">
            成功 {{ jobStats(item).success }}
          </ContentLink>
          <ContentLink class="subscription-stat failed" :to="completedTaskLink(item)">
            失败 {{ jobStats(item).failed }}
          </ContentLink>
          <button
            class="btn secondary small archive-restore"
            type="button"
            :disabled="busy === item.id"
            title="移出归档，恢复到订阅清单"
            @click="restore(item)"
          >
            <Loader2 v-if="busy === item.id" :size="14" class="spin" />
            <RotateCcw v-else :size="14" />
            移出归档
          </button>
        </div>
      </article>
    </div>

    <div v-if="selectionMode" class="selection-pill selection-pill-flat">
      <span class="selection-count">已选 {{ selectedCount }} 项</span>
      <button class="btn ghost small" type="button" :disabled="batchBusy" @click="allSelected ? clearSelection() : selectAll()">
        {{ allSelected ? '取消全选' : '全选' }}
      </button>
      <button class="btn ghost small" type="button" :disabled="batchBusy" @click="clearSelection">
        <X :size="14" />
        清空
      </button>
      <button class="btn ghost small" type="button" :disabled="batchBusy || !selectedCount" @click="batchRestore">
        <RotateCcw :size="14" />
        移除归档集
      </button>
    </div>
  </div>
</template>
