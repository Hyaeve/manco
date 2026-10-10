<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import {
  Activity,
  Bell,
  ChevronRight,
  CircleCheck,
  CircleUser,
  Compass,
  Download,
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
  Server,
  Settings,
  Sun,
  X,
} from 'lucide-vue-next'
import MancoLogo from './components/MancoLogo.vue'
import { api } from './api'
import { useAuthStore } from './stores/auth'
import { dismissNotice, notices } from './stores/notices'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const menuOpen = ref(false)
const openMenu = ref('')
const activityOpen = ref(false)
const activityItems = ref([])
const activityLoading = ref(false)

const theme = ref(localStorage.getItem('manco-theme') || 'system')
const themeOptions = [
  { id: 'light', label: '日光', icon: Sun },
  { id: 'dark', label: '夜间', icon: Moon },
  { id: 'system', label: '跟随系统', icon: Monitor },
]

const media = window.matchMedia('(prefers-color-scheme: dark)')

const navItems = [
  { name: 'dashboard', label: '总览', to: '/', icon: LayoutDashboard },
  { name: 'discover', label: '探索发现', to: '/discover', icon: Compass },
  { name: 'subscriptions', label: '订阅清单', to: '/subscriptions', icon: Rss },
  { name: 'downloads', label: '下载列表', to: '/downloads', icon: Download },
  { name: 'local', label: '本地库', to: '/local', icon: HardDrive },
  { name: 'sources', label: '资源库', to: '/sources', icon: Server },
]
const logsItem = { name: 'logs', label: '运行日志', to: '/logs', icon: ScrollText }
const systemItem = { name: 'settings', label: '系统设置', to: '/settings', icon: Settings }

const showShell = computed(() => route.name !== 'login')
const pageTitle = computed(() => {
  if (route.name === 'dashboard') return '总览'
  if (route.name === 'discover') return '探索发现'
  if (route.name === 'comic') return '作品详情'
  if (route.name === 'subscriptions') return '订阅清单'
  if (route.name === 'downloads') return '下载列表'
  if (route.name === 'local') return '本地库'
  if (route.name === 'sources') return '资源库'
  if (route.name === 'settings') return '系统设置'
  if (route.name === 'logs') return '运行日志'
  return 'Manco'
})
const breadcrumb = computed(() => {
  const list = [{ label: 'Manco', to: '/' }]
  if (route.name && route.name !== 'dashboard') list.push({ label: pageTitle.value })
  return list
})

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
  () => route.fullPath,
  () => {
    menuOpen.value = false
    openMenu.value = ''
    activityOpen.value = false
  },
)

onMounted(() => {
  applyTheme()
  media.addEventListener('change', applyTheme)
  if (!auth.ready) auth.load()
  window.addEventListener('click', closeMenus)
  window.addEventListener('keydown', escapeMenus)
})

onUnmounted(() => {
  media.removeEventListener('change', applyTheme)
  window.removeEventListener('click', closeMenus)
  window.removeEventListener('keydown', escapeMenus)
})

function closeMenus(event) {
  if (event && event.target?.closest?.('.topbar-menu')) return
  openMenu.value = ''
  activityOpen.value = false
}

function escapeMenus(event) {
  if (event.key === 'Escape') closeMenus()
}

async function toggleActivity(event) {
  event.stopPropagation()
  activityOpen.value = !activityOpen.value
  openMenu.value = ''
  if (!activityOpen.value) return
  activityLoading.value = true
  try {
    const payload = await api.activity()
    activityItems.value = payload.items || []
  } catch {
    activityItems.value = []
  } finally {
    activityLoading.value = false
  }
}

function toggleMenu(name, event) {
  event.stopPropagation()
  openMenu.value = openMenu.value === name ? '' : (activityOpen.value = false, name)
}

async function signOut() {
  await auth.logout()
  router.push({ name: 'login' })
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
        <MancoLogo class="brand-logo" :size="38" />
        <span class="brand-text">
          <strong>Manco</strong>
          <small>漫画书籍 · 订阅下载</small>
        </span>
      </div>
      <nav aria-label="主导航">
        <div class="nav-group">
          <RouterLink
            v-for="item in navItems"
            :key="item.name"
            :to="item.to"
            :aria-current="route.name === item.name ? 'page' : undefined"
            :class="{ active: route.name === item.name }"
          >
            <component :is="item.icon" :size="21" />
            <span>{{ item.label }}</span>
          </RouterLink>
          <RouterLink
            :to="logsItem.to"
            :aria-current="route.name === logsItem.name ? 'page' : undefined"
            :class="{ active: route.name === logsItem.name, 'nav-bottom': true }"
          >
            <component :is="logsItem.icon" :size="21" />
            <span>{{ logsItem.label }}</span>
          </RouterLink>
          <RouterLink
            :to="systemItem.to"
            :aria-current="route.name === systemItem.name ? 'page' : undefined"
            :class="{ active: route.name === systemItem.name }"
          >
            <component :is="systemItem.icon" :size="21" />
            <span>{{ systemItem.label }}</span>
          </RouterLink>
        </div>
      </nav>
    </aside>

    <div class="main-shell">
      <header class="topbar">
        <button class="icon-btn mobile-menu" type="button" aria-label="打开导航" @click.stop="menuOpen = !menuOpen">
          <Menu :size="20" />
        </button>
        <nav class="breadcrumbs" aria-label="面包屑">
          <template v-for="(crumb, index) in breadcrumb" :key="crumb.label">
            <ChevronRight v-if="index" :size="15" class="crumb-sep" />
            <RouterLink v-if="crumb.to" :to="crumb.to">{{ crumb.label }}</RouterLink>
            <strong v-else>{{ crumb.label }}</strong>
          </template>
        </nav>
        <div class="topbar-actions">
          <div class="theme-switch" role="group" aria-label="主题">
            <button
              v-for="option in themeOptions"
              :key="option.id"
              class="icon-btn"
              :class="{ selected: theme === option.id }"
              type="button"
              :aria-label="`主题：${option.label}`"
              :title="`主题：${option.label}`"
              @click="theme = option.id"
            >
              <component :is="option.icon" :size="18" />
            </button>
          </div>
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
              <ul v-else class="notification-list">
                <li v-for="entry in activityItems" :key="entry.id || entry.time">
                  <component
                    :is="activityIcon(entry.level)"
                    :size="16"
                    :class="entry.level === 'success' ? 'success-text' : entry.level === 'error' ? 'danger-text' : 'spin'"
                  />
                  <span>
                    <strong>{{ entry.message }}</strong>
                    <small>{{ entry.time ? new Date(entry.time).toLocaleString() : '' }}</small>
                  </span>
                </li>
              </ul>
            </section>
          </div>
          <div class="topbar-menu account-control" @click.stop>
            <button class="account-button" type="button" aria-label="账号菜单" @click="toggleMenu('account', $event)">
              <CircleUser :size="20" />
            </button>
            <div v-if="openMenu === 'account'" class="account-dropdown">
              <div class="account-name">
                <CircleUser :size="17" />
                <span>{{ auth.user?.username || '未登录' }}</span>
              </div>
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
      <main class="content-scroll">
        <div class="page-content">
          <RouterView />
        </div>
      </main>
    </div>
  </div>

  <TransitionGroup name="toast-slide" tag="div" class="toast-stack" aria-live="polite">
    <div
      v-for="notice in notices"
      :key="notice.id"
      class="toast"
      :class="{ error: notice.error }"
      :role="notice.error ? 'alert' : 'status'"
    >
      <span class="toast-symbol">
        <X v-if="notice.error" :size="15" />
        <CircleCheck v-else :size="15" />
      </span>
      <span>{{ notice.message }}</span>
      <button class="icon-btn" type="button" aria-label="关闭提示" @click="dismissNotice(notice.id)">
        <X :size="15" />
      </button>
    </div>
  </TransitionGroup>
</template>
