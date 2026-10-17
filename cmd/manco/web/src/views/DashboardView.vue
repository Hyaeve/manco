<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  AlertTriangle,
  BookOpen,
  Cat,
  CheckCircle2,
  Clock3,
  Download,
  Library,
  Loader2,
  Rss,
  Server,
} from 'lucide-vue-next'
import { api } from '../api'
import ContentLink from '../components/ContentLink.vue'
import SourceIcon from '../components/SourceIcon.vue'

const stats = ref({})
const latest = ref([])
const sources = ref([])
const downloads = ref([])
const upcoming = ref([])
const libraryTotal = ref(0)
const loading = ref(true)
const error = ref('')
const sourceTab = ref('comic')
const heroIndex = ref(0)
let heroTimer = 0

const sourceTabs = [
  { value: 'comic', label: '漫画源' },
  { value: 'book', label: '书籍源' },
  { value: 'other', label: '其他源' },
]

const heroComic = computed(() => latest.value[heroIndex.value] || null)
const heroCover = computed(() => heroComic.value ? localCoverUrl(heroComic.value, 1000) : '')
const heroStyle = computed(() => ({
  '--overview-hero-image': heroCover.value ? `url("${heroCover.value}")` : 'none',
}))
const activeTasks = computed(() =>
  downloads.value
    .filter((item) => ['running', 'queued', 'waiting', 'paused'].includes(String(item.status || '').toLowerCase()))
    .slice(0, 4),
)
const visibleSources = computed(() =>
  sources.value
    .filter((item) => !item.hidden && (item.kind || 'comic') === sourceTab.value)
    .slice(0, 8),
)

watch(
  () => latest.value.length,
  (count) => {
    if (heroIndex.value >= count) heroIndex.value = 0
    restartHeroRotation()
  },
)

onMounted(async () => {
  try {
    const [statsPayload, localPayload, downloadsPayload, sourcesPayload, upcomingPayload] = await Promise.all([
      api.stats(),
      api.localLibrary(7, 0),
      api.downloads(40, 0, 'active'),
      api.sources(),
      api.dashboardUpcoming(3),
    ])
    stats.value = statsPayload || {}
    latest.value = localPayload.items || []
    libraryTotal.value = Number(localPayload.total || latest.value.length)
    downloads.value = downloadsPayload.items || []
    sources.value = sourcesPayload.items || []
    upcoming.value = upcomingPayload.items || []
    restartHeroRotation()
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
})

onUnmounted(() => {
  stopHeroRotation()
})

function stopHeroRotation() {
  if (heroTimer) {
    window.clearInterval(heroTimer)
    heroTimer = 0
  }
}

function restartHeroRotation() {
  stopHeroRotation()
  if (latest.value.length <= 1) return
  heroTimer = window.setInterval(() => {
    heroIndex.value = (heroIndex.value + 1) % latest.value.length
  }, 6500)
}

function selectHero(index) {
  heroIndex.value = index
  restartHeroRotation()
}

function localCoverUrl(item, width = 480) {
  if (item?.cachedCover) return item.cachedCover
  if (item?.cover) return api.localFileUrl(item.cover, false, width)
  return ''
}

function subscriptionCover(item, width = 240) {
  if (item?.localCover) return api.localFileUrl(item.localCover, false, width)
  if (item?.cachedCover) return item.cachedCover
  return api.imageUrl(item?.cover, item?.sourceId, '', width)
}

function taskStatus(item) {
  const status = String(item?.status || '').toLowerCase()
  if (status === 'running') return { label: '下载中', className: 'running' }
  if (status === 'queued') return { label: '排队中', className: 'queued' }
  if (status === 'waiting' || status === 'paused') return { label: '等待中', className: 'waiting' }
  return { label: status || '等待', className: 'waiting' }
}

function formatNextRun(value) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '待计算'
  const pad = (number) => String(number).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}
</script>

<template>
  <div v-if="loading" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载信息总览</span>
  </div>
  <div v-else class="overview-dashboard">
    <div v-if="error" class="alert error">{{ error }}</div>

    <div class="overview-top">
      <section
        class="overview-window overview-hero"
        :style="heroStyle"
        @mouseenter="stopHeroRotation"
        @mouseleave="restartHeroRotation"
      >
        <div class="overview-hero-backdrop" />
        <template v-if="heroComic">
          <div class="overview-hero-content">
            <div class="overview-hero-poster">
              <img :src="heroCover" :alt="heroComic.title" />
            </div>
            <div class="overview-hero-copy">
              <span class="overview-kicker">最近下载</span>
              <h1>{{ heroComic.title }}</h1>
              <strong>{{ heroComic.author || '未知作者' }}</strong>
              <p>{{ heroComic.description || '暂无简介' }}</p>
              <ContentLink class="btn secondary small overview-hero-link" to="/local">
                <Library :size="15" />
                查看本地仓库
              </ContentLink>
            </div>
          </div>
          <div class="overview-hero-dots" aria-label="最近下载作品">
            <button
              v-for="(item, index) in latest"
              :key="item.path || item.title"
              type="button"
              :class="{ active: index === heroIndex }"
              :aria-label="`切换到 ${item.title}`"
              :title="item.title"
              @click="selectHero(index)"
            />
          </div>
        </template>
        <div v-else class="overview-hero-empty">
          <div class="overview-lazy-cat"><Cat :size="62" :stroke-width="1.45" /><span>z</span><span>z</span></div>
          <h2>本地书架还空着</h2>
          <p>下载完成的封面海报会在这里轮播。</p>
        </div>
      </section>

      <section class="overview-window overview-stats">
        <header class="overview-window-head">
          <div>
            <span class="overview-kicker">统计总览</span>
            <h2>Manco 运行概况</h2>
          </div>
          <Rss :size="19" />
        </header>
        <div class="overview-stat-grid">
          <div class="overview-stat">
            <Rss :size="17" />
            <strong>{{ stats.subscriptions || 0 }}</strong>
            <span>订阅作品</span>
          </div>
          <div class="overview-stat">
            <Library :size="17" />
            <strong>{{ libraryTotal }}</strong>
            <span>本地仓库</span>
          </div>
          <div class="overview-stat is-success">
            <CheckCircle2 :size="17" />
            <strong>{{ stats.completedJobs || 0 }}</strong>
            <span>完成章节</span>
          </div>
          <div class="overview-stat is-danger">
            <AlertTriangle :size="17" />
            <strong>{{ stats.failedJobs || 0 }}</strong>
            <span>失败章节</span>
          </div>
          <div class="overview-stat">
            <Download :size="17" />
            <strong>{{ stats.runningJobs || 0 }}</strong>
            <span>正在下载</span>
          </div>
          <div class="overview-stat">
            <Clock3 :size="17" />
            <strong>{{ stats.queuedJobs || 0 }}</strong>
            <span>排队章节</span>
          </div>
        </div>
      </section>
    </div>

    <div class="overview-bottom">
      <section class="overview-window overview-sources">
        <header class="overview-window-head">
          <div>
            <span class="overview-kicker">内容源</span>
            <h2>资源拓展</h2>
          </div>
          <ContentLink class="overview-head-link" to="/sources">管理</ContentLink>
        </header>
        <div class="overview-source-tabs" role="tablist" aria-label="内容源类型">
          <button
            v-for="tab in sourceTabs"
            :key="tab.value"
            type="button"
            :class="{ active: sourceTab === tab.value }"
            @click="sourceTab = tab.value"
          >
            {{ tab.label }}
          </button>
        </div>
        <div v-if="visibleSources.length" class="overview-source-grid">
          <ContentLink
            v-for="item in visibleSources"
            :key="item.id"
            class="overview-source-item"
            :to="{ name: 'discover', query: { kind: item.kind || 'comic', source: item.id } }"
            :title="item.name"
          >
            <span class="overview-source-icon">
              <SourceIcon :src="item.icon" :kind="item.kind || 'comic'" :alt="item.name" :size="26" />
            </span>
            <span>{{ item.name }}</span>
          </ContentLink>
        </div>
        <div v-else class="overview-mini-empty">
          <Server :size="28" />
          <span>当前分类没有内容源</span>
        </div>
      </section>

      <section class="overview-window overview-tasks">
        <header class="overview-window-head">
          <div>
            <span class="overview-kicker">任务列表</span>
            <h2>下载队列</h2>
          </div>
          <ContentLink class="overview-head-link" to="/downloads">查看全部</ContentLink>
        </header>
        <div v-if="activeTasks.length" class="overview-task-list">
          <ContentLink
            v-for="item in activeTasks"
            :key="item.id"
            class="overview-task-row"
            to="/downloads"
          >
            <span class="overview-task-state" :class="taskStatus(item).className">
              {{ taskStatus(item).label }}
            </span>
            <span class="overview-task-copy">
              <strong>{{ item.comicTitle || '未命名作品' }}</strong>
              <small>{{ item.chapterTitle || '章节' }}</small>
            </span>
          </ContentLink>
        </div>
        <div v-else class="overview-lazy-empty">
          <div class="overview-lazy-cat is-sleeping">
            <Cat :size="58" :stroke-width="1.42" />
            <span>z</span>
            <span>Z</span>
          </div>
          <strong>今天没有任务</strong>
          <p>猫咪正在打盹，下载队列很安静。</p>
        </div>
      </section>

      <section class="overview-window overview-upcoming">
        <header class="overview-window-head">
          <div>
            <span class="overview-kicker">订阅计划</span>
            <h2>即将检查更新</h2>
          </div>
          <ContentLink class="overview-head-link" to="/subscriptions">订阅清单</ContentLink>
        </header>
        <div v-if="upcoming.length" class="overview-upcoming-list">
          <ContentLink
            v-for="item in upcoming"
            :key="item.id"
            class="overview-upcoming-card"
            :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.comicId } }"
          >
            <span class="overview-upcoming-cover">
              <img v-if="subscriptionCover(item)" :src="subscriptionCover(item)" :alt="item.title" />
              <BookOpen v-else :size="22" />
            </span>
            <span class="overview-upcoming-copy">
              <strong>{{ item.title }}</strong>
              <small>{{ item.author || '未知作者' }}</small>
              <span class="overview-upcoming-description">{{ item.description || '暂无简介' }}</span>
              <span class="overview-upcoming-cron">
                <Clock3 :size="13" />
                {{ formatNextRun(item.nextRun) }} · {{ item.cronExpr }}
              </span>
            </span>
          </ContentLink>
        </div>
        <div v-else class="overview-mini-empty">
          <Clock3 :size="28" />
          <span>没有设置 Cron 的启用订阅</span>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.overview-dashboard {
  display: grid;
  gap: 16px;
}

.overview-top {
  display: grid;
  grid-template-columns: minmax(0, 1.75fr) minmax(300px, 0.85fr);
  gap: 16px;
}

.overview-bottom {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

.overview-window {
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
  box-shadow: var(--shadow-sm);
}

.overview-hero {
  position: relative;
  min-height: 374px;
  color: #fff;
  background: #332c25;
}

.overview-hero-backdrop {
  position: absolute;
  inset: -24px;
  background-image: var(--overview-hero-image);
  background-position: center;
  background-size: cover;
  filter: blur(25px) saturate(0.9);
  opacity: 0.66;
  transform: scale(1.1);
}

.overview-hero::after {
  position: absolute;
  inset: 0;
  content: '';
  background: linear-gradient(90deg, rgba(22, 19, 16, 0.82), rgba(22, 19, 16, 0.54) 56%, rgba(22, 19, 16, 0.32));
}

.overview-hero-content {
  position: relative;
  z-index: 1;
  display: grid;
  grid-template-columns: 176px minmax(0, 1fr);
  gap: 24px;
  min-height: 374px;
  align-items: center;
  padding: 28px 34px;
}

.overview-hero-poster {
  width: 176px;
  aspect-ratio: 2 / 3;
  overflow: hidden;
  border: 1px solid rgba(255, 255, 255, 0.22);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.1);
  box-shadow: 0 18px 38px rgba(0, 0, 0, 0.32);
}

.overview-hero-poster img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.overview-hero-copy {
  display: grid;
  align-content: center;
  gap: 9px;
  min-width: 0;
}

.overview-kicker {
  color: var(--text-muted);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.overview-hero-copy .overview-kicker {
  color: rgba(255, 255, 255, 0.72);
}

.overview-hero-copy h1 {
  margin: 0;
  color: #fff;
  font-size: clamp(25px, 2.4vw, 38px);
  font-weight: 760;
  line-height: 1.15;
}

.overview-hero-copy strong {
  color: rgba(255, 255, 255, 0.86);
  font-size: 14px;
  font-weight: 550;
}

.overview-hero-copy p {
  display: -webkit-box;
  max-width: 660px;
  margin: 2px 0 5px;
  overflow: hidden;
  color: rgba(255, 255, 255, 0.78);
  font-size: 13px;
  line-height: 1.68;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 4;
}

.overview-hero-link {
  justify-self: start;
}

.overview-hero-dots {
  position: absolute;
  right: 22px;
  bottom: 18px;
  z-index: 2;
  display: flex;
  gap: 8px;
}

.overview-hero-dots button {
  width: 8px;
  height: 8px;
  padding: 0;
  border: 0;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.35);
  cursor: pointer;
  transition: width 0.18s ease, background 0.18s ease;
}

.overview-hero-dots button.active {
  width: 22px;
  background: #fff;
}

.overview-hero-empty {
  position: relative;
  z-index: 1;
  display: grid;
  min-height: 374px;
  place-items: center;
  align-content: center;
  gap: 8px;
  text-align: center;
}

.overview-hero-empty h2,
.overview-hero-empty p {
  margin: 0;
}

.overview-hero-empty p {
  color: rgba(255, 255, 255, 0.7);
}

.overview-stats {
  padding: 18px;
}

.overview-window-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.overview-window-head h2 {
  margin: 3px 0 0;
  color: var(--text);
  font-size: 17px;
  font-weight: 700;
}

.overview-head-link {
  color: var(--primary-strong);
  font-size: 12px;
  font-weight: 600;
}

.overview-stat-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  margin-top: 18px;
}

.overview-stat {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  gap: 3px 9px;
  min-height: 74px;
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--surface-muted);
}

.overview-stat svg {
  grid-row: 1 / span 2;
  color: var(--primary-strong);
}

.overview-stat strong {
  font-size: 22px;
  line-height: 1;
}

.overview-stat span {
  color: var(--text-muted);
  font-size: 11px;
}

.overview-stat.is-success svg {
  color: #4f8b61;
}

.overview-stat.is-danger svg {
  color: var(--danger);
}

.overview-bottom > .overview-window {
  display: flex;
  height: 360px;
  flex-direction: column;
  padding: 16px;
  overflow: hidden;
}

.overview-source-tabs {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 4px;
  margin-top: 14px;
  padding: 4px;
  border-radius: 9px;
  background: var(--surface-muted);
}

.overview-source-tabs button {
  min-height: 30px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
}

.overview-source-tabs button.active {
  background: var(--surface);
  box-shadow: 0 2px 8px rgba(45, 38, 30, 0.1);
  color: var(--text);
  font-weight: 650;
}

.overview-source-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px 8px;
  margin-top: 17px;
}

.overview-source-item {
  display: grid;
  justify-items: center;
  gap: 7px;
  min-width: 0;
  color: var(--text);
  font-size: 11px;
  text-align: center;
}

.overview-source-item > span:last-child {
  width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.overview-source-icon {
  display: grid;
  width: 42px;
  height: 42px;
  place-items: center;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface-muted);
}

.overview-task-list,
.overview-upcoming-list {
  display: grid;
  gap: 8px;
  margin-top: 14px;
}

.overview-task-row {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  gap: 10px;
  min-height: 40px;
  padding: 7px 9px;
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text);
}

.overview-task-state {
  min-width: 48px;
  padding: 3px 7px;
  border-radius: 999px;
  background: var(--surface-muted);
  color: var(--text-muted);
  font-size: 10px;
  text-align: center;
}

.overview-task-state.running {
  background: color-mix(in srgb, #75a57d 26%, var(--surface));
  color: #3f714a;
}

.overview-task-state.queued,
.overview-task-state.waiting {
  background: color-mix(in srgb, #d9ad5d 24%, var(--surface));
  color: #7a5c22;
}

.overview-task-copy,
.overview-upcoming-copy {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.overview-task-copy strong,
.overview-upcoming-copy strong {
  overflow: hidden;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.overview-task-copy small,
.overview-upcoming-copy small {
  overflow: hidden;
  color: var(--text-muted);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.overview-lazy-empty,
.overview-mini-empty {
  display: grid;
  flex: 1;
  place-items: center;
  align-content: center;
  gap: 7px;
  color: var(--text-muted);
  text-align: center;
}

.overview-lazy-empty strong {
  color: var(--text);
  font-size: 13px;
}

.overview-lazy-empty p,
.overview-mini-empty span {
  margin: 0;
  font-size: 11px;
}

.overview-lazy-cat {
  position: relative;
  display: grid;
  width: 92px;
  height: 80px;
  place-items: center;
  color: #b77b45;
  background: color-mix(in srgb, var(--primary-soft) 58%, var(--surface));
  border-radius: 42% 48% 38% 46%;
}

.overview-lazy-cat.is-sleeping svg {
  transform: rotate(8deg) translateY(5px);
}

.overview-lazy-cat span {
  position: absolute;
  right: 8px;
  color: color-mix(in srgb, var(--text-muted) 70%, transparent);
  font-size: 13px;
  font-weight: 700;
}

.overview-lazy-cat span:nth-of-type(1) {
  top: 13px;
}

.overview-lazy-cat span:nth-of-type(2) {
  top: 0;
  right: 22px;
  font-size: 10px;
}

.overview-upcoming-card {
  display: grid;
  grid-template-columns: 46px minmax(0, 1fr);
  gap: 9px;
  min-height: 76px;
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text);
}

.overview-upcoming-cover {
  display: grid;
  width: 46px;
  height: 64px;
  place-items: center;
  overflow: hidden;
  border-radius: 6px;
  background: var(--surface-muted);
}

.overview-upcoming-cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.overview-upcoming-description {
  overflow: hidden;
  color: var(--text-muted);
  font-size: 10px;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.overview-upcoming-cron {
  display: flex;
  align-items: center;
  gap: 4px;
  overflow: hidden;
  color: color-mix(in srgb, var(--primary-strong) 74%, var(--text-muted));
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 1180px) {
  .overview-top {
    grid-template-columns: 1fr;
  }

  .overview-bottom {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .overview-bottom > :last-child {
    grid-column: 1 / -1;
  }
}

@media (max-width: 760px) {
  .overview-bottom {
    grid-template-columns: 1fr;
  }

  .overview-bottom > :last-child {
    grid-column: auto;
  }

  .overview-hero-content {
    grid-template-columns: 112px minmax(0, 1fr);
    gap: 16px;
    padding: 22px 18px;
  }

  .overview-hero-poster {
    width: 112px;
  }

  .overview-hero-copy p {
    -webkit-line-clamp: 3;
  }

  .overview-source-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

:global([data-theme='dark']) .overview-stat,
:global([data-theme='dark']) .overview-source-tabs,
:global([data-theme='dark']) .overview-source-icon,
:global([data-theme='dark']) .overview-upcoming-cover {
  background: #302a22;
}

:global([data-theme='dark']) .overview-hero {
  background: #191714;
}
</style>
