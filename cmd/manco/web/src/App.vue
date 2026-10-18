<script setup>
import { computed, defineComponent, h, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import {
  Activity,
  Bell,
  CircleCheck,
  UserRound,
  Compass,
  HardDrive,
  Info,
  LayoutDashboard,
  LoaderCircle,
  LogOut,
  Menu,
  Monitor,
  Moon,
  Rss,
  ScrollText,
  Search,
  Server,
  Settings,
  Sun,
  X,
} from 'lucide-vue-next'
import ConfirmDialog from './components/ConfirmDialog.vue'
import { api } from './api'
import { useAuthStore } from './stores/auth'
import { dismissNotice, notices } from './stores/notices'

// 任务列表图标：向下箭头落入下载托盘，纯线条绘制。
const TaskListIcon = defineComponent({
  name: 'TaskListIcon',
  props: { size: { type: [Number, String], default: 21 } },
  setup(props) {
    return () =>
      h(
        'svg',
        {
          xmlns: 'http://www.w3.org/2000/svg',
          width: props.size,
          height: props.size,
          viewBox: '0 0 24 24',
          fill: 'none',
          stroke: 'currentColor',
          'stroke-width': 1.8,
          'stroke-linecap': 'round',
          'stroke-linejoin': 'round',
          'aria-hidden': 'true',
        },
        [
          h('path', { d: 'M12 3.5v9.2' }),
          h('path', { d: 'm8.6 9.3 3.4 3.4 3.4-3.4' }),
          h('path', { d: 'M4 14.4v4.1a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-4.1' }),
          h('path', { d: 'M4 14.4h4.3l1.1 2.1h5.2l1.1-2.1H20' }),
        ],
      )
  },
})

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const menuOpen = ref(false)
const openMenu = ref('')
const activityOpen = ref(false)
const activityItems = ref([])
// 切页面时给内容区加一次浮入动画的类（KeepAlive 缓存下动画不会自动重播）。
const pageAnimating = ref(false)
let pageAnimTimer = 0
// 通知列表分片渲染：先 12 条，滚到底再追加，避免一次性铺满 DOM。
const ACTIVITY_PAGE_SIZE = 12
const activityRenderLimit = ref(ACTIVITY_PAGE_SIZE)
const activityListRef = ref(null)
const visibleActivity = computed(() => activityItems.value.slice(0, activityRenderLimit.value))

function handleActivityScroll() {
  const element = activityListRef.value
  if (!element) return
  if (activityRenderLimit.value >= activityItems.value.length) return
  if (element.scrollTop + element.clientHeight >= element.scrollHeight - 80) {
    activityRenderLimit.value += ACTIVITY_PAGE_SIZE
  }
}
const activityLoading = ref(false)
const searchQuery = ref('')
const searchInput = ref(null)
const SEARCH_HISTORY_KEY = 'manco.search.history.v1'
let sessionCheckTimer = 0
let activityTimer = 0
let prefetchHandle = 0

const theme = ref(localStorage.getItem('manco-theme') || 'light')
const themeOptions = [
  { id: 'light', label: '日光', icon: Sun },
  { id: 'dark', label: '夜间', icon: Moon },
  { id: 'system', label: '跟随系统', icon: Monitor },
]
const activeTheme = computed(() => themeOptions.find((item) => item.id === theme.value) || themeOptions[0])

const media = window.matchMedia('(prefers-color-scheme: dark)')

const navItems = [
  { name: 'dashboard', label: '信息总览', to: '/', icon: LayoutDashboard },
  { name: 'discover', label: '探索发现', to: '/discover', icon: Compass },
  { name: 'subscriptions', label: '订阅计划', to: '/subscriptions', icon: Rss },
  { name: 'downloads', label: '任务列表', to: '/downloads', icon: TaskListIcon },
  { name: 'local', label: '本地仓库', to: '/local', icon: HardDrive },
  { name: 'sources', label: '资源拓展', to: '/sources', icon: Server },
]
const logsItem = { name: 'logs', label: '运行日志', to: '/logs', icon: ScrollText }
const systemItem = { name: 'settings', label: '系统设置', to: '/settings', icon: Settings }

const showShell = computed(() => route.name !== 'login')

// 侧栏切换时保持各主页面实例与数据，回到页面不再触发新请求。
const cachedViews = [
  'DashboardView',
  'DiscoverView',
  'SubscriptionsView',
  'DownloadsView',
  'ArchivedSubscriptionsView',
  'CompletedTasksView',
  'LocalLibraryView',
  'SourcesView',
  'LogsView',
  'SettingsView',
]

const mainViewLoaders = [
  () => import('./views/DashboardView.vue'),
  () => import('./views/DiscoverView.vue'),
  () => import('./views/SubscriptionsView.vue'),
  () => import('./views/DownloadsView.vue'),
  () => import('./views/ArchivedSubscriptionsView.vue'),
  () => import('./views/CompletedTasksView.vue'),
  () => import('./views/LocalLibraryView.vue'),
  () => import('./views/SourcesView.vue'),
  () => import('./views/LogsView.vue'),
  () => import('./views/SettingsView.vue'),
]

function prefetchMainViews() {
  for (const load of mainViewLoaders) {
    void load().catch(() => {})
  }
}

const scrollPositions = new Map()
let pendingScrollRestore = 0
let scrollContainer = null
let scrollCaptureSuspended = false

function restoreScrollPosition() {
  const name = route.name
  if (!name) {
    scrollCaptureSuspended = false
    return
  }
  if (pendingScrollRestore) cancelAnimationFrame(pendingScrollRestore)
  pendingScrollRestore = requestAnimationFrame(() => {
    pendingScrollRestore = 0
    const container = document.querySelector('.content-scroll')
    if (container) container.scrollTop = scrollPositions.get(name) || 0
    requestAnimationFrame(() => {
      scrollCaptureSuspended = false
    })
  })
}

function captureContentScroll(name = route.name) {
  const container = document.querySelector('.content-scroll')
  if (name && container) scrollPositions.set(name, container.scrollTop)
}

function handleShellScroll() {
  if (!scrollCaptureSuspended) captureContentScroll()
}

// 归档集归在订阅清单栏目下，已完成归在任务列表栏目下。
function navActive(name) {
  if (name === 'subscriptions') return route.name === 'subscriptions' || route.name === 'archive'
  if (name === 'downloads') return route.name === 'downloads' || route.name === 'completed'
  return route.name === name
}

function applyTheme() {
  const resolved = theme.value === 'system' ? (media.matches ? 'dark' : 'light') : theme.value
  document.documentElement.dataset.theme = resolved
  document.documentElement.dataset.themePreference = theme.value
}

watch(theme, (value) => {
  localStorage.setItem('manco-theme', value)
  applyTheme()
})

watch(
  () => [route.name, route.fullPath],
  async ([toName], [fromName]) => {
    menuOpen.value = false
    openMenu.value = ''
    activityOpen.value = false
    // 每次切页面重播一次浮入动画（KeepAlive 缓存的视图不会自己重新挂载）。
    pageAnimating.value = false
    window.clearTimeout(pageAnimTimer)
    await nextTick()
    pageAnimating.value = true
    pageAnimTimer = window.setTimeout(() => {
      pageAnimating.value = false
    }, 200)
    if (fromName !== toName) {
      await nextTick()
      restoreScrollPosition()
    }
  },
)

const removeScrollCaptureGuard = router.beforeEach((to, from) => {
  captureContentScroll(from.name)
  scrollCaptureSuspended = from.name !== to.name
  return true
})

onMounted(() => {
  applyTheme()
  media.addEventListener('change', applyTheme)
  if (!auth.ready) auth.load()
  scrollContainer = document.querySelector('.content-scroll')
  scrollContainer?.addEventListener('scroll', handleShellScroll, { passive: true })
  window.addEventListener('click', closeMenus)
  window.addEventListener('keydown', handleGlobalKeys)
  window.addEventListener('focus', checkSession)
  window.addEventListener('manco:unauthorized', handleUnauthorized)
  document.addEventListener('visibilitychange', checkSessionOnVisible)
  sessionCheckTimer = window.setInterval(checkSession, 30000)
  activityTimer = window.setInterval(() => {
    if (route.name !== 'login') loadActivity(true)
  }, 15000)
  if (route.name !== 'login') loadActivity(true)
  if (route.name !== 'login') {
    if ('requestIdleCallback' in window) {
      prefetchHandle = window.requestIdleCallback(prefetchMainViews, { timeout: 3000 })
    } else {
      prefetchHandle = window.setTimeout(prefetchMainViews, 1200)
    }
  }
})

onUnmounted(() => {
  media.removeEventListener('change', applyTheme)
  removeScrollCaptureGuard()
  scrollContainer?.removeEventListener('scroll', handleShellScroll)
  scrollContainer = null
  window.removeEventListener('click', closeMenus)
  window.removeEventListener('keydown', handleGlobalKeys)
  window.removeEventListener('focus', checkSession)
  window.removeEventListener('manco:unauthorized', handleUnauthorized)
  document.removeEventListener('visibilitychange', checkSessionOnVisible)
  if (sessionCheckTimer) window.clearInterval(sessionCheckTimer)
  if (activityTimer) window.clearInterval(activityTimer)
  if (prefetchHandle) {
    if ('cancelIdleCallback' in window) window.cancelIdleCallback(prefetchHandle)
    else window.clearTimeout(prefetchHandle)
  }
  if (pendingScrollRestore) cancelAnimationFrame(pendingScrollRestore)
})

function closeMenus(event) {
  if (event && event.target?.closest?.('.topbar-menu')) return
  openMenu.value = ''
  activityOpen.value = false
}

function escapeMenus(event) {
  if (event.key === 'Escape') closeMenus()
}

function handleGlobalKeys(event) {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
    event.preventDefault()
    searchInput.value?.focus()
    searchInput.value?.select?.()
    return
  }
  if (event.key === 'Escape') {
    closeMenus()
    if (route.name === 'search') router.push({ name: 'discover' })
  }
}

function submitGlobalSearch() {
  const query = searchQuery.value.trim()
  if (!query) return
  let history = []
  try {
    history = JSON.parse(localStorage.getItem(SEARCH_HISTORY_KEY) || '[]')
  } catch {}
  if (!Array.isArray(history)) history = []
  localStorage.setItem(SEARCH_HISTORY_KEY, JSON.stringify([query, ...history.filter((item) => item !== query)].slice(0, 3)))
  router.push({ name: 'search', query: { q: query } })
}

function clearGlobalSearch() {
  searchQuery.value = ''
  if (route.name === 'search') router.push({ name: 'discover' })
}

async function loadActivity(silent = false) {
  if (!silent) activityLoading.value = true
  try {
    const payload = await api.activity()
    activityItems.value = payload.items || []
  } catch {
    if (!silent) activityItems.value = []
  } finally {
    activityLoading.value = false
  }
}

function toggleActivity(event) {
  event.stopPropagation()
  activityOpen.value = !activityOpen.value
  openMenu.value = ''
  if (activityOpen.value) {
    // 每次打开都从第一页开始渲染。
    activityRenderLimit.value = ACTIVITY_PAGE_SIZE
    loadActivity()
  }
}

function toggleMenu(name, event) {
  event.stopPropagation()
  openMenu.value = openMenu.value === name ? '' : (activityOpen.value = false, name)
}

async function signOut() {
  await auth.logout()
  router.replace({ name: 'login' })
}

function handleUnauthorized() {
  auth.invalidate()
  if (route.name === 'login') return
  const redirect = route.fullPath && route.fullPath !== '/' ? route.fullPath : undefined
  router.replace({ name: 'login', query: redirect ? { redirect } : {} })
}

async function checkSession() {
  if (route.name === 'login') return
  try {
    await api.me()
  } catch (err) {
    if (err.status === 401) handleUnauthorized()
  }
}

function checkSessionOnVisible() {
  if (document.visibilityState === 'visible') checkSession()
}

function cycleTheme() {
  const index = themeOptions.findIndex((item) => item.id === theme.value)
  theme.value = themeOptions[(index + 1) % themeOptions.length].id
}

function goSection(section) {
  router.push({ name: 'settings', query: { section } })
}

function activityIcon(level) {
  if (level === 'success') return CircleCheck
  if (level === 'error') return Activity
  return LoaderCircle
}
</script>

<template>
  <RouterView v-if="!showShell" />
  <div v-else class="app-shell">
    <div v-if="menuOpen" class="nav-overlay" @click="menuOpen = false" />
    <aside class="sidebar" :class="{ open: menuOpen }">
      <div class="brand">
        <span class="brand-mark">
          <img class="brand-lockup brand-lockup-light" src="/brand-lockup.png" alt="Manco 漫画书籍订阅下载" />
          <img class="brand-lockup brand-lockup-dark" src="/brand-lockup-plate.png" alt="Manco 漫画书籍订阅下载" />
          <!-- 副标题对齐横条图里 “Manco” 字标的左边，而不是整条图的左边。 -->
          <small class="brand-tagline">漫画书籍 · 订阅下载</small>
        </span>
      </div>
      <nav aria-label="主导航">
        <div class="nav-group">
          <button
            v-for="item in navItems"
            :key="item.name"
            type="button"
            :aria-current="navActive(item.name) ? 'page' : undefined"
            :class="{ active: navActive(item.name) }"
            @click="router.push(item.to)"
          >
            <component :is="item.icon" :size="21" />
            <span>{{ item.label }}</span>
          </button>
          <button
            type="button"
            :aria-current="route.name === logsItem.name ? 'page' : undefined"
            :class="{ active: route.name === logsItem.name, 'nav-bottom': true }"
            @click="router.push(logsItem.to)"
          >
            <component :is="logsItem.icon" :size="21" />
            <span>{{ logsItem.label }}</span>
          </button>
          <button
            type="button"
            :aria-current="route.name === systemItem.name ? 'page' : undefined"
            :class="{ active: route.name === systemItem.name }"
            @click="router.push(systemItem.to)"
          >
            <component :is="systemItem.icon" :size="21" />
            <span>{{ systemItem.label }}</span>
          </button>
        </div>
      </nav>
    </aside>

    <div class="main-shell">
      <header class="topbar">
        <button class="icon-btn mobile-menu" type="button" aria-label="打开导航" @click.stop="menuOpen = !menuOpen">
          <Menu :size="20" />
        </button>
        <form class="global-search" role="search" @submit.prevent="submitGlobalSearch">
          <Search :size="16" />
          <input
            ref="searchInput"
            v-model="searchQuery"
            class="global-search-input"
            type="search"
            placeholder="搜索漫画、书籍或资源"
            autocomplete="off"
          />
          <kbd>Ctrl K</kbd>
          <button v-if="searchQuery" class="global-search-clear" type="button" aria-label="清除搜索" @click="clearGlobalSearch">
            <X :size="15" />
          </button>
        </form>
        <div class="topbar-actions">
          <button
            class="icon-btn theme-cycle"
            type="button"
            :aria-label="`当前主题：${activeTheme.label}`"
            :title="`主题：${activeTheme.label}（点击切换）`"
            @click="cycleTheme"
          >
            <component :is="activeTheme.icon" :size="18" />
          </button>
          <div class="topbar-menu notification-control" @click.stop>
            <button class="icon-btn notification-button" type="button" aria-label="活动通知" @click="toggleActivity">
              <Bell :size="20" />
              <span v-if="activityItems.length" class="notification-badge">
                {{ activityItems.length > 99 ? '99+' : activityItems.length }}
              </span>
            </button>
            <section v-if="activityOpen" class="notification-dropdown">
              <header>
                <h2>活动通知</h2>
                <Activity :size="15" />
              </header>
              <p v-if="activityLoading" class="small-empty">加载中…</p>
              <p v-else-if="!activityItems.length" class="small-empty">暂无活动记录</p>
      <ul v-else ref="activityListRef" class="notification-list" @scroll="handleActivityScroll">
        <li v-for="entry in visibleActivity" :key="entry.id || entry.time">
          <!-- 作品相关的通知用封面，不再用图标 -->
          <img v-if="entry.cover" class="notification-cover" :src="entry.cover" :alt="entry.comicTitle" loading="lazy" decoding="async" />
          <component
            v-else
            :is="activityIcon(entry.level)"
            :size="16"
            :class="entry.ongoing && !entry.total ? 'spin' : entry.level === 'success' ? 'success-text' : entry.level === 'error' ? 'danger-text' : ''"
          />
                  <span>
                    <strong>{{ entry.comicTitle || '未命名作品' }}</strong>
                    <span class="notification-summary">
                      成功 {{ entry.succeeded || 0 }}
                      <template v-if="entry.failed"> · 失败 {{ entry.failed }}</template>
                      <template v-if="entry.pending"> · 待处理 {{ entry.pending }}</template>
                    </span>
                    <small>{{ entry.time ? new Date(entry.time).toLocaleString() : '' }}</small>
                  </span>
                </li>
              </ul>
            </section>
          </div>
          <div class="topbar-menu account-control" @click.stop>
            <button class="account-button" type="button" aria-label="账号菜单" @click="toggleMenu('account', $event)">
              <UserRound :size="20" />
            </button>
            <div v-if="openMenu === 'account'" class="account-dropdown">
              <button type="button" @click="goSection('account')">
                <Settings :size="17" /> 账号安全
              </button>
              <button type="button" @click="goSection('about')">
                <Info :size="17" /> 关于 Manco
              </button>
              <button type="button" class="danger" @click="signOut">
                <LogOut :size="17" /> 退出登录
              </button>
            </div>
          </div>
        </div>
      </header>
    <main class="content-scroll" :class="{ 'logs-page': route.name === 'logs', 'page-enter': pageAnimating }">
        <div class="page-content">
          <RouterView v-slot="{ Component }">
            <KeepAlive :include="cachedViews">
              <component :is="Component" />
            </KeepAlive>
          </RouterView>
        </div>
      </main>
    </div>
  </div>

  <ConfirmDialog />
  <TransitionGroup name="toast-slide" tag="div" class="toast-stack" aria-live="polite">
    <div
      v-for="notice in notices"
      :key="notice.id"
      class="toast"
      :class="notice.type || (notice.error ? 'error' : 'info')"
      :role="notice.type === 'error' ? 'alert' : 'status'"
    >
      <span class="toast-symbol">
        <X v-if="notice.type === 'error'" :size="15" />
        <CircleCheck v-else :size="15" />
      </span>
      <span>{{ notice.message }}</span>
      <button class="icon-btn" type="button" aria-label="关闭提示" @click="dismissNotice(notice.id)">
        <X :size="15" />
      </button>
    </div>
  </TransitionGroup>
</template>
