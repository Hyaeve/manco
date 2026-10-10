<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import {
  Activity,
  Bell,
  ChevronRight,
  CircleUser,
  Compass,
  Download,
  HardDrive,
  Info,
  LayoutDashboard,
  LogOut,
  Menu,
  Moon,
  Rss,
  Server,
  Settings,
  ScrollText,
  Sun,
  SunMoon,
} from 'lucide-vue-next'
import MancoLogo from './components/MancoLogo.vue'
import { api } from './api'
import { useAuthStore } from './stores/auth'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const menuOpen = ref(false)
const openMenu = ref('')
const activityOpen = ref(false)
const activityItems = ref([])
const activityLoading = ref(false)
const theme = ref(localStorage.getItem('manco-theme') || 'light')

const navItems = [
  { name: 'dashboard', label: '总览', to: '/', icon: LayoutDashboard },
  { name: 'discover', label: '探索发现', to: '/discover', icon: Compass },
  { name: 'subscriptions', label: '订阅清单', to: '/subscriptions', icon: Rss },
  { name: 'downloads', label: '下载列表', to: '/downloads', icon: Download },
  { name: 'local', label: '本地库', to: '/local', icon: HardDrive },
  { name: 'sources', label: '资源库', to: '/sources', icon: Server },
]
const systemItem = { name: 'settings', label: '系统设置', to: '/settings', icon: Settings }
const logsItem = { name: 'logs', label: '运行日志', to: '/logs', icon: ScrollText }

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

function applyTheme(value) {
  theme.value = value
  document.documentElement.dataset.theme = value
  localStorage.setItem('manco-theme', value)
}

function cycleTheme() {
  const order = ['light', 'dark', 'auto']
  const next = order[(order.indexOf(theme.value) + 1) % order.length]
  applyTheme(next)
}

const themeIcon = computed(() => {
  if (theme.value === 'dark') return Moon
  if (theme.value === 'auto') return SunMoon
  return Sun
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
  applyTheme(theme.value)
  if (!auth.ready) auth.load()
  window.addEventListener('click', closeMenus)
})

onUnmounted(() => {
  window.removeEventListener('click', closeMenus)
})

function closeMenus(event) {
  if (event.target.closest('.topbar-menu')) return
  openMenu.value = ''
  activityOpen.value = false
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
  openMenu.value = openMenu.value === name ? '' : name
  activityOpen.value = false
}

async function signOut() {
  await auth.logout()
  router.push({ name: 'login' })
}

function goSection(section) {
  router.push({ name: 'settings', query: { section } })
}
</script>

<template>
  <RouterView v-if="!showShell" />
  <div v-else class="app-shell">
    <aside class="sidebar" :class="{ open: menuOpen }">
      <div class="brand">
        <MancoLogo class="brand-logo" :size="34" />
        <span class="brand-text">
          <strong>Manco</strong>
          <span>漫画书籍订阅下载</span>
        </span>
      </div>
      <nav class="nav">
        <RouterLink v-for="item in navItems" :key="item.name" :to="item.to">
          <component :is="item.icon" :size="17" />
          <span>{{ item.label }}</span>
        </RouterLink>
      </nav>
      <div class="sidebar-foot">
        <nav class="nav sidebar-settings">
          <RouterLink :to="logsItem.to">
            <component :is="logsItem.icon" :size="17" />
            <span>{{ logsItem.label }}</span>
          </RouterLink>
          <RouterLink :to="systemItem.to">
            <component :is="systemItem.icon" :size="17" />
            <span>{{ systemItem.label }}</span>
          </RouterLink>
        </nav>
      </div>
    </aside>
    <div v-if="menuOpen" class="scrim" @click="menuOpen = false" />
    <main class="main">
      <header class="topbar">
        <div class="inline">
          <button class="btn ghost icon mobile-only" type="button" aria-label="打开导航" @click="menuOpen = true">
            <Menu :size="20" />
          </button>
          <nav class="breadcrumb" aria-label="面包屑">
            <template v-for="(crumb, index) in breadcrumb" :key="crumb.label">
              <ChevronRight v-if="index" :size="14" class="crumb-sep" />
              <RouterLink v-if="crumb.to" :to="crumb.to">{{ crumb.label }}</RouterLink>
              <span v-else class="crumb-current">{{ crumb.label }}</span>
            </template>
          </nav>
        </div>
        <div class="topbar-actions">
          <button class="btn ghost icon topbar-menu" type="button" :title="`主题：${theme}`" @click="cycleTheme">
            <component :is="themeIcon" :size="18" />
          </button>
          <div class="topbar-menu menu-anchor">
            <button class="btn ghost icon" type="button" title="活动通知" @click="toggleActivity">
              <Bell :size="18" />
              <span v-if="activityItems.length" class="notif-dot" />
            </button>
            <div v-if="activityOpen" class="dropdown-panel activity-panel" @click.stop>
              <div class="dropdown-head">
                <strong>活动通知</strong>
                <Activity :size="15" />
              </div>
              <div v-if="activityLoading" class="dropdown-empty">加载中…</div>
              <div v-else-if="!activityItems.length" class="dropdown-empty">暂无活动记录</div>
              <ul v-else class="activity-list">
                <li v-for="entry in activityItems" :key="entry.id || entry.time">
                  <span class="activity-dot" :class="entry.level || 'info'" />
                  <div>
                    <p>{{ entry.message }}</p>
                    <span class="muted small">{{ entry.time ? new Date(entry.time).toLocaleString() : '' }}</span>
                  </div>
                </li>
              </ul>
            </div>
          </div>
          <div class="topbar-menu menu-anchor">
            <button class="btn ghost icon" type="button" title="账号" @click="toggleMenu('account', $event)">
              <CircleUser :size="19" />
            </button>
            <div v-if="openMenu === 'account'" class="dropdown-panel account-panel" @click.stop>
              <div class="dropdown-user">
                <CircleUser :size="18" />
                <span>{{ auth.user?.username || '未登录' }}</span>
              </div>
              <button class="dropdown-item" type="button" @click="goSection('account')">
                <Settings :size="15" />
                账号安全
              </button>
              <button class="dropdown-item" type="button" @click="goSection('about')">
                <Info :size="15" />
                关于 Manco
              </button>
              <button class="dropdown-item danger" type="button" @click="signOut">
                <LogOut :size="15" />
                退出登录
              </button>
            </div>
          </div>
        </div>
      </header>
      <RouterView />
    </main>
  </div>
</template>
