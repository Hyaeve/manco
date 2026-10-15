<script setup>
import { onMounted, ref } from 'vue'
import { Archive, ArrowLeft, BookOpen, Loader2, RefreshCw, RotateCcw } from 'lucide-vue-next'
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

onMounted(load)

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
      <article v-for="item in items" :key="item.id" class="archive-card">
        <ContentLink class="archive-cover" :to="archiveComicLink(item)" :title="item.title">
          <img v-if="cover(item)" :src="cover(item)" :alt="item.title" loading="lazy" />
          <BookOpen v-else :size="22" />
        </ContentLink>
        <div class="archive-meta">
          <ContentLink class="archive-title" :to="archiveComicLink(item)" :title="item.title">
            {{ item.title }}
          </ContentLink>
          <span class="archive-sub">{{ item.author || '未知作者' }}</span>
          <span class="archive-sub">
            {{ chapterCount(item) }} 话 · 最新章节：{{ item.lastChapterTitle || '尚未记录' }}
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
  </div>
</template>
