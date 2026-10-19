<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  AlertTriangle,
  ArrowDownToLine,
  Blocks,
  BookOpen,
  CalendarClock,
  Cat,
  ChartNoAxesCombined,
  CheckCircle2,
  Clock3,
  Library,
  ListChecks,
  ListOrdered,
  Loader2,
  Rss,
  Server,
  Tag,
  UserRound,
} from 'lucide-vue-next'
import { api } from '../api'
import ContentLink from '../components/ContentLink.vue'
import SourceIcon from '../components/SourceIcon.vue'
import { splitAuthors } from '../utils/metadata'
import { SOURCES_CHANGED_EVENT } from '../utils/sourceEvents'

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
const heroHovering = ref(false)
const heroDirection = ref('next')
let heroTimer = 0
const HERO_ROTATION_MS = 16000

const sourceTabs = [
  { value: 'comic', label: '漫画源' },
  { value: 'book', label: '书籍源' },
  { value: 'other', label: '视频源' },
]

const heroComic = computed(() => latest.value[heroIndex.value] || null)
const heroAuthors = computed(() => splitAuthors(heroComic.value?.author))
const heroKey = computed(() =>
  latest.value[heroIndex.value]?.path
  || latest.value[heroIndex.value]?.comicId
  || latest.value[heroIndex.value]?.title
  || 'empty',
)
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
  window.addEventListener(SOURCES_CHANGED_EVENT, refreshSources)
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
  window.removeEventListener(SOURCES_CHANGED_EVENT, refreshSources)
  stopHeroRotation()
})

async function refreshSources() {
  try {
    const payload = await api.sources()
    sources.value = payload.items || []
  } catch {
    // 保持当前来源列表，下一次页面轮询或手动刷新会重新获取。
  }
}

function stopHeroRotation() {
  if (heroTimer) {
    window.clearInterval(heroTimer)
    heroTimer = 0
  }
}

function restartHeroRotation() {
  stopHeroRotation()
  if (heroHovering.value || latest.value.length <= 1) return
  heroTimer = window.setInterval(() => {
    heroDirection.value = 'next'
    heroIndex.value = (heroIndex.value + 1) % latest.value.length
  }, HERO_ROTATION_MS)
}

function selectHero(index) {
  if (index === heroIndex.value) return
  heroDirection.value = index < heroIndex.value ? 'prev' : 'next'
  heroIndex.value = index
  restartHeroRotation()
}

function pauseHeroRotation() {
  heroHovering.value = true
  stopHeroRotation()
}

function resumeHeroRotation() {
  heroHovering.value = false
  restartHeroRotation()
}

function localCoverUrl(item, width = 480) {
  if (item?.cover) return api.localFileUrl(item.cover, false, width)
  if (item?.cachedCover) return item.cachedCover
  if (item?.sourceCover) return api.imageUrl(item.sourceCover, item?.sourceId, '', width)
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

function progressPercent(item) {
  if (item?.status === 'completed') return 100
  const total = Number(item?.totalPages || 0)
  const completed = Number(item?.completedPages || 0)
  if (total <= 0) return item?.status === 'running' ? 6 : 0
  return Math.max(0, Math.min(100, Math.round((completed / total) * 100)))
}

function startOfCalendarDay(date) {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate())
}

function startOfCalendarWeek(date) {
  const start = startOfCalendarDay(date)
  const offset = (start.getDay() + 6) % 7
  start.setDate(start.getDate() - offset)
  return start
}

function formatNextRun(value) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '待计算'
  const pad = (number) => String(number).padStart(2, '0')
  const now = new Date()
  const dayDiff = Math.round((startOfCalendarDay(date) - startOfCalendarDay(now)) / 86400000)
  const clock = `${pad(date.getHours())}:${pad(date.getMinutes())}`
  if (dayDiff === 0) return `今天 ${clock}`
  if (dayDiff === 1) return `明天 ${clock}`
  if (dayDiff === 2) return `后天 ${clock}`
  const weekdays = ['一', '二', '三', '四', '五', '六', '日']
  const weekday = weekdays[(date.getDay() + 6) % 7]
  const weekDiff = Math.round((startOfCalendarWeek(date) - startOfCalendarWeek(now)) / 604800000)
  if (weekDiff === 0) return `周${weekday} ${clock}`
  if (weekDiff === 1) return `下周${weekday} ${clock}`
  return `${date.getMonth() + 1}/${date.getDate()} ${clock}`
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
        :class="{ 'is-empty': !heroComic }"
        @mouseenter="pauseHeroRotation"
        @mouseleave="resumeHeroRotation"
      >
        <Transition :name="`overview-hero-bg-${heroDirection}`">
          <div
            v-if="heroComic"
            :key="heroKey"
            class="overview-hero-backdrop"
            :style="heroStyle"
          />
        </Transition>
        <template v-if="heroComic">
          <div class="overview-hero-slide-stage">
            <Transition :name="`overview-hero-slide-${heroDirection}`">
              <div :key="heroKey" class="overview-hero-content">
                <div class="overview-hero-poster" :class="{ blurred: heroComic.coverBlur }">
                  <img :src="heroCover" :alt="heroComic.title" />
                </div>
                <div class="overview-hero-copy">
                  <h1>{{ heroComic.title }}</h1>
                  <div class="overview-hero-authors">
                    <template v-if="heroAuthors.length">
                      <span v-for="name in heroAuthors" :key="name" class="author-pill">
                        <UserRound :size="12" :stroke-width="1.9" />
                        {{ name }}
                      </span>
                    </template>
                    <span v-else class="author-pill">
                      <UserRound :size="12" :stroke-width="1.9" />
                      未知作者
                    </span>
                  </div>
                  <p>{{ heroComic.description || '暂无简介' }}</p>
                  <div v-if="heroComic.tags?.length" class="tag-list overview-hero-tags" aria-label="作品标签">
                    <span v-for="tag in heroComic.tags.slice(0, 10)" :key="tag" class="badge tag-pill">
                      <Tag :size="12" />
                      {{ tag }}
                    </span>
                  </div>
                  <ContentLink class="btn secondary small overview-hero-link" to="/local">
                    <Library :size="15" />
                    查看本地仓库
                  </ContentLink>
                </div>
              </div>
            </Transition>
          </div>
          <div class="overview-hero-dots" aria-label="最近下载作品">
            <button
              v-for="(item, index) in latest"
              :key="item.path || item.title"
              type="button"
              :class="{ active: index === heroIndex }"
              :aria-label="`切换到 ${item.title}`"
              :title="item.title"
              @mouseenter="selectHero(index)"
              @focus="selectHero(index)"
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
          <div class="overview-window-title">
            <ChartNoAxesCombined :size="30" :stroke-width="1.8" />
            <span>统计总览</span>
          </div>
        </header>
        <div class="overview-stat-grid">
          <div class="overview-stat">
            <Rss :size="30" :stroke-width="1.65" />
            <strong>{{ stats.subscriptions || 0 }}</strong>
            <span>订阅作品</span>
          </div>
          <div class="overview-stat">
            <Library :size="30" :stroke-width="1.65" />
            <strong>{{ libraryTotal }}</strong>
            <span>本地仓库</span>
          </div>
          <div class="overview-stat is-success">
            <CheckCircle2 :size="30" :stroke-width="1.65" />
            <strong>{{ stats.completedJobs || 0 }}</strong>
            <span>完成章节</span>
          </div>
          <div class="overview-stat is-danger">
            <AlertTriangle :size="30" :stroke-width="1.65" />
            <strong>{{ stats.failedJobs || 0 }}</strong>
            <span>失败章节</span>
          </div>
          <div class="overview-stat">
            <ArrowDownToLine :size="30" :stroke-width="1.65" />
            <strong>{{ stats.runningJobs || 0 }}</strong>
            <span>正在下载</span>
          </div>
          <div class="overview-stat">
            <ListOrdered :size="30" :stroke-width="1.65" />
            <strong>{{ stats.queuedJobs || 0 }}</strong>
            <span>排队章节</span>
          </div>
        </div>
      </section>
    </div>

    <div class="overview-bottom">
      <section class="overview-window overview-sources">
        <header class="overview-window-head">
          <ContentLink class="overview-window-title" to="/sources">
            <Blocks :size="30" :stroke-width="1.8" />
            <span>资源拓展</span>
          </ContentLink>
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
              <SourceIcon :src="item.icon" :kind="item.kind || 'comic'" :alt="item.name" :size="30" />
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
          <ContentLink class="overview-window-title" to="/downloads">
            <ListChecks :size="30" :stroke-width="1.8" />
            <span>下载队列</span>
          </ContentLink>
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
              <span
                class="overview-task-progress"
                :class="`is-${taskStatus(item).className}`"
                role="progressbar"
                :aria-valuenow="progressPercent(item)"
                aria-valuemin="0"
                aria-valuemax="100"
              >
                <span :style="{ width: `${progressPercent(item)}%` }" />
              </span>
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
          <ContentLink class="overview-window-title" to="/subscriptions">
            <CalendarClock :size="30" :stroke-width="1.8" />
            <span>订阅计划</span>
          </ContentLink>
          <ContentLink class="overview-head-link" to="/subscriptions">查看全部</ContentLink>
        </header>
        <div v-if="upcoming.length" class="overview-upcoming-list">
          <ContentLink
            v-for="item in upcoming"
            :key="item.id"
            class="overview-upcoming-card"
            to="/subscriptions"
          >
            <span class="overview-upcoming-cover" :class="{ blurred: item.coverBlur }">
              <img v-if="subscriptionCover(item)" :src="subscriptionCover(item)" :alt="item.title" />
              <BookOpen v-else :size="22" />
            </span>
            <span class="overview-upcoming-copy">
              <strong>{{ item.title }}</strong>
              <small>{{ item.author || '未知作者' }}</small>
              <span class="overview-upcoming-cron">
                <Clock3 :size="14" />
                {{ formatNextRun(item.nextRun) }}
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
  width: 100%;
  height: calc(100dvh - 92px);
  min-width: 0;
  grid-template-rows: minmax(0, 0.53fr) minmax(0, 0.47fr);
  gap: 12px;
}

.overview-top {
  display: grid;
  grid-template-columns: minmax(0, 1.75fr) minmax(300px, 0.85fr);
  gap: 12px;
  min-height: 0;
}

.overview-bottom {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  min-height: 0;
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
  height: 100%;
  min-height: 0;
  color: var(--text);
  background: color-mix(in srgb, #fffef8 97%, #fbf4cf);
}

.overview-hero-backdrop {
  position: absolute;
  inset: -30px;
  background-image: var(--overview-hero-image);
  background-position: center;
  background-size: cover;
  filter: blur(18px) saturate(1.08) brightness(1.06);
  opacity: 0.46;
  transform: scale(1.16);
  will-change: opacity, transform;
}

.overview-hero-bg-next-enter-active,
.overview-hero-bg-next-leave-active,
.overview-hero-bg-prev-enter-active,
.overview-hero-bg-prev-leave-active {
  transition: opacity 0.92s cubic-bezier(0.22, 1, 0.36, 1), transform 1.18s cubic-bezier(0.22, 1, 0.36, 1);
}

.overview-hero-bg-next-enter-from,
.overview-hero-bg-prev-enter-from {
  opacity: 0;
}

.overview-hero-bg-next-leave-to,
.overview-hero-bg-prev-leave-to {
  opacity: 0;
}

.overview-hero-bg-next-enter-from {
  transform: translateX(4%) scale(1.12);
}

.overview-hero-bg-next-leave-to {
  transform: translateX(-4%) scale(1.06);
}

.overview-hero-bg-prev-enter-from {
  transform: translateX(-4%) scale(1.12);
}

.overview-hero-bg-prev-leave-to {
  transform: translateX(4%) scale(1.06);
}

.overview-hero-slide-stage {
  position: relative;
  z-index: 1;
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

.overview-hero-slide-next-enter-active,
.overview-hero-slide-next-leave-active,
.overview-hero-slide-prev-enter-active,
.overview-hero-slide-prev-leave-active {
  transition:
    opacity 0.66s ease,
    filter 0.66s ease,
    transform 0.82s cubic-bezier(0.22, 0.82, 0.24, 1);
}

.overview-hero-slide-next-enter-from,
.overview-hero-slide-prev-enter-from,
.overview-hero-slide-next-leave-to,
.overview-hero-slide-prev-leave-to {
  opacity: 0;
  filter: blur(4px);
}

.overview-hero-slide-next-enter-from {
  transform: translate3d(7%, 0, 0);
}

.overview-hero-slide-next-leave-to {
  transform: translate3d(-7%, 0, 0);
}

.overview-hero-slide-prev-enter-from {
  transform: translate3d(-7%, 0, 0);
}

.overview-hero-slide-prev-leave-to {
  transform: translate3d(7%, 0, 0);
}

.overview-hero.is-empty {
  color: var(--text);
  background:
    linear-gradient(135deg, color-mix(in srgb, var(--primary-soft) 58%, var(--surface)), var(--surface) 58%),
    var(--surface);
}

.overview-hero.is-empty::after {
  background:
    radial-gradient(circle at 50% 42%, color-mix(in srgb, var(--primary) 11%, transparent), transparent 34%),
    linear-gradient(135deg, transparent, color-mix(in srgb, var(--primary-soft) 28%, transparent));
}

.overview-hero.is-empty .overview-hero-empty h2 {
  color: var(--text);
}

.overview-hero.is-empty .overview-hero-empty p {
  color: var(--text-muted);
}

.overview-hero.is-empty .overview-lazy-cat {
  background: color-mix(in srgb, var(--primary-soft) 78%, var(--surface));
  box-shadow: 0 16px 38px color-mix(in srgb, var(--primary) 12%, transparent);
}

.overview-hero.is-empty .overview-hero-empty {
  color: var(--text);
}

.overview-hero.is-empty::before {
  position: absolute;
  inset: 18px;
  border: 1px dashed color-mix(in srgb, var(--primary) 18%, var(--border));
  border-radius: 9px;
  content: '';
  pointer-events: none;
}

.overview-hero::after {
  position: absolute;
  inset: 0;
  content: '';
  background:
    linear-gradient(90deg, rgba(255, 254, 248, 0.93), rgba(255, 251, 238, 0.84) 54%, rgba(255, 253, 247, 0.68)),
    color-mix(in srgb, var(--surface) 88%, transparent);
  backdrop-filter: blur(6px) saturate(1.08);
  -webkit-backdrop-filter: blur(6px) saturate(1.08);
}

.overview-hero-content {
  position: absolute;
  inset: 0;
  z-index: 1;
  display: grid;
  grid-template-columns: clamp(138px, 13vw, 168px) minmax(0, 1fr);
  gap: 20px;
  height: 100%;
  min-height: 0;
  align-items: center;
  padding: 18px 26px 22px;
  will-change: opacity, filter, transform;
}

.overview-hero-poster {
  position: relative;
  align-self: end;
  justify-self: start;
  width: clamp(138px, 13vw, 168px);
  aspect-ratio: 2 / 3;
  overflow: hidden;
  border: 1px solid rgba(255, 255, 255, 0.82);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.5);
  box-shadow: 0 18px 38px rgba(91, 66, 39, 0.2);
}

.overview-hero-poster img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  transform: scale(1.01);
  transition: filter 0.42s ease, transform 0.6s cubic-bezier(0.22, 1, 0.36, 1);
}

.overview-hero-poster.blurred img {
  filter: blur(var(--manco-cover-blur, 6px)) saturate(0.94);
  transform: scale(var(--manco-cover-blur-scale, 1.03));
}

.overview-hero-poster.blurred:hover img,
.overview-hero-poster.blurred:focus-within img {
  filter: blur(0);
  transform: scale(var(--manco-cover-hover-scale, 1.08));
}

.overview-hero-poster.blurred::after {
  position: absolute;
  inset: 0;
  z-index: 1;
  border-radius: inherit;
  background:
    repeating-linear-gradient(
      135deg,
      rgba(255, 255, 255, 0.1) 0 1px,
      rgba(255, 255, 255, 0) 1px 4px
    ),
    linear-gradient(135deg, rgba(255, 255, 255, 0.16), rgba(87, 76, 60, 0.12));
  backdrop-filter: blur(1.5px) saturate(0.84);
  -webkit-backdrop-filter: blur(1.5px) saturate(0.84);
  content: '';
  pointer-events: none;
  transition: opacity 0.32s ease;
}

.overview-hero-poster.blurred:hover::after,
.overview-hero-poster.blurred:focus-within::after {
  opacity: 0;
}

.overview-hero-copy {
  display: grid;
  align-content: center;
  gap: 6px;
  min-width: 0;
}

.overview-hero-copy h1 {
  margin: 0;
  color: var(--text);
  font-size: clamp(24px, 2.1vw, 32px);
  font-weight: 760;
  line-height: 1.15;
}

.overview-hero-authors {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.overview-hero-copy p {
  display: -webkit-box;
  max-width: 660px;
  margin: 2px 0 5px;
  overflow: hidden;
  color: var(--text);
  font-size: 15px;
  line-height: 1.6;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.overview-hero-tags :deep(.tag-pill) {
  max-width: 150px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.overview-hero-tags :deep(.tag-pill svg) {
  flex: 0 0 auto;
}

.overview-hero-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  max-height: 48px;
  margin-bottom: 0;
  overflow: hidden;
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
  gap: 9px;
  padding: 4px;
}

.overview-hero-dots button {
  width: 8px;
  height: 8px;
  padding: 0;
  border: 0;
  border-radius: 999px;
  background: color-mix(in srgb, var(--primary-strong) 28%, transparent);
  cursor: pointer;
  transform: translateZ(0);
  transition:
    width 0.34s cubic-bezier(0.22, 1.35, 0.36, 1),
    background-color 0.24s ease,
    box-shadow 0.24s ease,
    transform 0.24s ease;
  will-change: width, transform;
}

.overview-hero-dots button.active,
.overview-hero-dots button:hover,
.overview-hero-dots button:focus-visible {
  width: 22px;
  background: var(--primary-strong);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--primary) 18%, transparent);
  outline: none;
  transform: scaleY(1.08);
}

.overview-hero-empty {
  position: relative;
  z-index: 1;
  display: grid;
  height: 100%;
  min-height: 0;
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
  display: flex;
  height: 100%;
  min-height: 0;
  flex-direction: column;
  padding: 12px 14px;
  overflow: hidden;
}

.overview-window-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.overview-window-title {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: 10px;
  color: var(--text);
  font-size: 20px;
  font-weight: 720;
  line-height: 1.2;
}

.overview-window-title svg {
  flex: 0 0 auto;
  width: 23px;
  height: 23px;
  color: var(--primary-strong);
}

.overview-window-title > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.overview-head-link {
  color: var(--primary-strong);
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
}

.overview-stat-grid {
  display: grid;
  flex: 1;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  grid-template-rows: repeat(3, minmax(0, 1fr));
  gap: 8px;
  min-height: 0;
  margin-top: 10px;
}

.overview-stat {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  gap: 2px 10px;
  min-height: 0;
  padding: 8px 12px 8px 16px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--surface-muted);
}

.overview-stat svg {
  grid-row: 1 / span 2;
  width: 30px;
  height: 30px;
  color: var(--primary-strong);
  filter: drop-shadow(0 2px 5px color-mix(in srgb, var(--primary) 16%, transparent));
}

.overview-stat strong {
  font-size: 20px;
  line-height: 1;
}

.overview-stat span {
  color: var(--text-muted);
  font-size: 10px;
}

.overview-stat.is-success svg {
  color: #4f8b61;
}

.overview-stat.is-danger svg {
  color: var(--danger);
}

.overview-bottom > .overview-window {
  display: flex;
  height: 100%;
  flex-direction: column;
  min-height: 0;
  padding: 14px;
  overflow: hidden;
}

.overview-source-tabs {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 4px;
  margin-top: 10px;
  padding: 4px;
  border-radius: 9px;
  background: var(--surface-muted);
}

.overview-source-tabs button {
  min-height: 28px;
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
  flex: 1;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  grid-template-rows: repeat(2, minmax(0, 1fr));
  gap: 6px 8px;
  min-height: 0;
  margin-top: 10px;
}

.overview-source-item {
  display: grid;
  justify-items: center;
  align-content: center;
  gap: 4px;
  min-width: 0;
  padding: 2px 3px;
  border-radius: 8px;
  color: var(--text);
  font-size: 11px;
  text-align: center;
  transition: background-color 0.2s ease, transform 0.2s ease;
}

.overview-source-item:hover {
  background: color-mix(in srgb, var(--primary-soft) 44%, transparent);
  transform: translateY(-1px);
}

.overview-source-item > span:last-child {
  width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.overview-source-icon {
  display: grid;
  width: 50px;
  height: 50px;
  flex: 0 0 50px;
  place-items: center;
  border: 0;
  border-radius: 0;
  background: transparent;
}

.overview-source-icon :deep(img) {
  width: 48px;
  height: 48px;
  border-radius: 14px;
  object-fit: contain;
  box-shadow: 0 5px 14px rgba(48, 39, 28, 0.14);
}

.overview-source-icon :deep(svg) {
  width: 44px;
  height: 44px;
  padding: 9px;
  border-radius: 14px;
  background: var(--surface-muted);
}

.overview-task-list,
.overview-upcoming-list {
  display: grid;
  flex: 1;
  grid-auto-rows: minmax(0, 1fr);
  gap: 6px;
  min-height: 0;
  margin-top: 10px;
}

.overview-task-row {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  gap: 8px;
  min-height: 0;
  padding: 6px 8px;
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text);
  transition: border-color 0.2s ease, background-color 0.2s ease, transform 0.2s ease;
}

.overview-task-row:hover,
.overview-upcoming-card:hover {
  border-color: color-mix(in srgb, var(--primary) 38%, var(--border));
  background: color-mix(in srgb, var(--primary-soft) 22%, var(--surface));
  transform: translateY(-1px);
}

.overview-task-state {
  min-width: 44px;
  padding: 3px 7px;
  border-radius: 999px;
  background: var(--surface-muted);
  color: var(--text-muted);
  font-size: 9.5px;
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

.overview-task-copy {
  gap: 3px;
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

.overview-task-progress {
  position: relative;
  display: block;
  width: 100%;
  height: 4px;
  margin-top: 0;
  overflow: hidden;
  border-radius: 999px;
  background: color-mix(in srgb, var(--text-muted) 18%, transparent);
}

.overview-task-progress > span {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--primary);
  transition: width 0.35s ease;
}

.overview-task-progress.is-waiting > span {
  background: #7c9fc4;
}

.overview-task-progress.is-running > span {
  box-shadow: 0 0 8px color-mix(in srgb, var(--primary) 45%, transparent);
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
  grid-template-columns: 58px minmax(0, 1fr);
  gap: 9px;
  min-height: 0;
  padding: 6px;
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text);
  transition: border-color 0.2s ease, background-color 0.2s ease, transform 0.2s ease;
}

.overview-upcoming-cover {
  position: relative;
  display: grid;
  width: auto;
  height: 100%;
  aspect-ratio: 2 / 3;
  justify-self: center;
  place-items: center;
  overflow: hidden;
  border-radius: 6px;
  background: var(--surface-muted);
}

.overview-upcoming-cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  transition: filter 0.42s ease, transform 0.42s ease;
}

.overview-upcoming-cover.blurred img {
  filter: blur(var(--manco-cover-blur, 6px)) saturate(0.94);
  transform: scale(var(--manco-cover-blur-scale, 1.03));
}

.overview-upcoming-cover.blurred:hover img,
.overview-upcoming-card:focus-visible .overview-upcoming-cover.blurred img {
  filter: blur(0);
  transform: scale(var(--manco-cover-hover-scale, 1.08));
}

.overview-upcoming-cron {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-top: 1px;
  overflow: hidden;
  color: color-mix(in srgb, var(--primary-strong) 74%, var(--text-muted));
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 1180px) {
  .overview-dashboard {
    height: auto;
    grid-template-rows: none;
  }

  .overview-top {
    grid-template-columns: 1fr;
    height: auto;
  }

  .overview-hero {
    height: clamp(260px, 34vh, 330px);
  }

  .overview-stats {
    height: auto;
  }

  .overview-stat-grid {
    flex: none;
    grid-template-rows: repeat(3, minmax(58px, auto));
  }

  .overview-bottom {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    height: auto;
  }

  .overview-bottom > .overview-window {
    height: clamp(300px, 38vh, 350px);
  }

  .overview-bottom > :last-child {
    grid-column: 1 / -1;
  }
}

@media (max-width: 760px) {
  .overview-dashboard,
  .overview-top,
  .overview-bottom {
    min-width: 0;
  }

  .overview-bottom {
    grid-template-columns: 1fr;
  }

  .overview-window-title {
    gap: 8px;
    font-size: 18px;
  }

  .overview-window-title svg {
    width: 20px;
    height: 20px;
  }

  .overview-bottom > :last-child {
    grid-column: auto;
  }

  .overview-hero-content {
    grid-template-columns: 92px minmax(0, 1fr);
    gap: 14px;
    padding: 20px 16px 22px;
  }

  .overview-hero-poster {
    align-self: end;
    width: 92px;
  }

  .overview-hero-copy {
    gap: 8px;
  }

  .overview-hero-copy h1 {
    font-size: 23px;
    line-height: 1.18;
  }

  .overview-hero-copy strong {
    font-size: 12px;
  }

  .overview-hero-copy p {
    margin-bottom: 1px;
    font-size: 12px;
    line-height: 1.55;
    -webkit-line-clamp: 3;
  }

  .overview-hero-tags {
    gap: 4px;
  }

  .overview-hero-tags span {
    max-width: 108px;
    padding: 2px 7px;
    font-size: 10px;
  }

  .overview-hero-dots {
    right: 14px;
    bottom: 18px;
  }

  .overview-source-grid {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
}

:global([data-theme='dark'] .overview-stat),
:global([data-theme='dark'] .overview-source-tabs),
:global([data-theme='dark'] .overview-source-icon),
:global([data-theme='dark'] .overview-upcoming-cover) {
  background: #302a22;
}

:global([data-theme='dark'] .overview-hero) {
  color: #fff;
  background: #191714;
}

:global([data-theme='dark'] .overview-hero-backdrop) {
  inset: -24px;
  filter: blur(14px) saturate(0.9);
  opacity: 0.66;
  transform: scale(1.1);
}

:global([data-theme='dark'] .overview-hero::after) {
  background: linear-gradient(90deg, rgba(22, 19, 16, 0.82), rgba(22, 19, 16, 0.54) 56%, rgba(22, 19, 16, 0.32));
}

:global([data-theme='dark'] .overview-hero-copy h1) {
  color: #fff;
}

:global([data-theme='dark'] .overview-hero-copy strong) {
  color: rgba(255, 255, 255, 0.86);
}

:global([data-theme='dark'] .overview-hero-copy p) {
  color: rgba(255, 255, 255, 0.78);
}

:global([data-theme='dark'] .overview-hero-poster) {
  border-color: rgba(255, 255, 255, 0.22);
  background: rgba(255, 255, 255, 0.1);
  box-shadow: 0 18px 38px rgba(0, 0, 0, 0.32);
}

:global([data-theme='dark'] .overview-hero-dots button) {
  background: rgba(255, 255, 255, 0.35);
}

:global([data-theme='dark'] .overview-hero-dots button.active),
:global([data-theme='dark'] .overview-hero-dots button:hover),
:global([data-theme='dark'] .overview-hero-dots button:focus-visible) {
  background: #fff;
  box-shadow: 0 0 0 3px rgba(255, 255, 255, 0.12);
}

:global([data-theme='dark'] .overview-hero.is-empty::after) {
  background:
    radial-gradient(circle at 50% 42%, rgba(183, 123, 69, 0.12), transparent 34%),
    linear-gradient(135deg, rgba(255, 255, 255, 0.02), rgba(183, 123, 69, 0.06));
}

@media (prefers-reduced-motion: reduce) {
  .overview-hero-bg-next-enter-active,
  .overview-hero-bg-next-leave-active,
  .overview-hero-bg-prev-enter-active,
  .overview-hero-bg-prev-leave-active,
  .overview-hero-slide-next-enter-active,
  .overview-hero-slide-next-leave-active,
  .overview-hero-slide-prev-enter-active,
  .overview-hero-slide-prev-leave-active {
    transition-duration: 0.01ms;
  }
}
</style>
