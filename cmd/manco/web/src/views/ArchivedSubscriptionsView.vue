<script setup>
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { Archive, BookOpen, Loader2, RefreshCw } from 'lucide-vue-next'
import { api } from '../api'
import SourceIcon from '../components/SourceIcon.vue'
import { notify } from '../stores/notices'

const items = ref([])
const sources = ref({})
const jobStatsData = ref({})
const loading = ref(true)

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
        <Archive :size="18" />
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
        <RouterLink class="archive-cover" :to="archiveComicLink(item)" :title="item.title">
          <img v-if="item.cover" :src="cover(item)" :alt="item.title" loading="lazy" />
          <BookOpen v-else :size="22" />
        </RouterLink>
        <div class="archive-meta">
          <RouterLink class="archive-title" :to="archiveComicLink(item)" :title="item.title">
            {{ item.title }}
          </RouterLink>
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
          <RouterLink class="subscription-stat success" :to="completedTaskLink(item)">
            成功 {{ jobStats(item).success }}
          </RouterLink>
          <RouterLink class="subscription-stat failed" :to="completedTaskLink(item)">
            失败 {{ jobStats(item).failed }}
          </RouterLink>
        </div>
      </article>
    </div>
  </div>
</template>
