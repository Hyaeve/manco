<script setup>
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { CircleCheck, Download, Library, Loader2, Plus, Rss, Server } from 'lucide-vue-next'
import { api } from '../api'

const stats = ref(null)
const downloads = ref([])
const sources = ref([])
const loading = ref(true)
const error = ref('')

onMounted(async () => {
  try {
    const [statsPayload, downloadsPayload, sourcesPayload] = await Promise.all([
      api.stats(),
      api.downloads(8),
      api.sources(),
    ])
    stats.value = statsPayload
    downloads.value = downloadsPayload.items || []
    sources.value = sourcesPayload.items || []
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
})

function statusClass(status) {
  if (status === 'completed') return 'success'
  if (status === 'running') return 'primary'
  if (status === 'failed') return 'danger'
  if (status === 'canceled') return 'warning'
  return ''
}
</script>

<template>
  <div v-if="loading" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载中</span>
  </div>
  <div v-else>
    <div v-if="error" class="alert error">{{ error }}</div>

    <div class="stat-grid">
      <div class="card stat">
        <span class="stat-icon"><Rss :size="18" /></span>
        <span>
          <span class="stat-value">{{ stats?.subscriptions ?? 0 }}</span>
          <span class="stat-label">订阅作品</span>
        </span>
      </div>
      <div class="card stat">
        <span class="stat-icon"><Download :size="18" /></span>
        <span>
          <span class="stat-value">{{ stats?.activeJobs ?? 0 }}</span>
          <span class="stat-label">进行中任务</span>
        </span>
      </div>
      <div class="card stat">
        <span class="stat-icon"><CircleCheck :size="18" /></span>
        <span>
          <span class="stat-value">{{ stats?.completedJobs ?? 0 }}</span>
          <span class="stat-label">已完成章节</span>
        </span>
      </div>
      <div class="card stat">
        <span class="stat-icon"><Library :size="18" /></span>
        <span>
          <span class="stat-value">{{ stats?.libraryItems ?? 0 }}</span>
          <span class="stat-label">本地 CBZ</span>
        </span>
      </div>
    </div>

    <section class="section">
      <div class="section-head">
        <h2>漫画源</h2>
        <RouterLink class="btn secondary small" to="/sources">
          <Server :size="15" />
          管理漫画源
        </RouterLink>
      </div>
      <div class="card card-pad inline">
        <RouterLink
          v-for="item in sources"
          :key="item.id"
          class="badge primary"
          :to="{ name: 'discover', query: { source: item.id } }"
        >
          {{ item.name }}
        </RouterLink>
        <span v-if="!sources.length" class="muted small">尚未加载漫画源</span>
      </div>
    </section>

    <section class="section">
      <div class="section-head">
        <h2>最近下载</h2>
        <RouterLink class="btn secondary small" to="/downloads">
          <Plus :size="15" />
          新建下载
        </RouterLink>
      </div>
      <div class="card table-wrap">
        <table>
          <thead>
            <tr>
              <th>作品</th>
              <th>章节</th>
              <th>状态</th>
              <th>进度</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="job in downloads" :key="job.id">
              <td>{{ job.comicTitle }}</td>
              <td class="muted">{{ job.chapterTitle }}</td>
              <td><span class="badge" :class="statusClass(job.status)">{{ job.status }}</span></td>
              <td>
                <div class="progress" v-if="job.totalPages">
                  <span :style="{ width: `${Math.min(100, (job.completedPages / job.totalPages) * 100)}%` }" />
                </div>
                <span v-else class="muted small">{{ job.completedPages }} 页</span>
              </td>
            </tr>
            <tr v-if="!downloads.length">
              <td colspan="4" class="muted" style="text-align: center">还没有下载任务</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>
