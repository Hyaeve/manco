<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import {
  Compass,
  Download,
  LayoutDashboard,
  Library,
  LogOut,
  Menu,
  Rss,
  ScrollText,
  Server,
  Settings,
  User,
} from 'lucide-vue-next'
import { useAuthStore } from './stores/auth'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const menuOpen = ref(false)

const navItems = [
  { name: 'dashboard', label: '总览', to: '/', icon: LayoutDashboard },
  { name: 'discover', label: '发现', to: '/discover', icon: Compass },
  { name: 'subscriptions', label: '订阅', to: '/subscriptions', icon: Rss },
  { name: 'downloads', label: '下载', to: '/downloads', icon: Download },
  { name: 'library', label: '资料库', to: '/library', icon: Library },
  { name: 'sources', label: '漫画源', to: '/sources', icon: Server },
  { name: 'logs', label: '系统日志', to: '/logs', icon: ScrollText },
]

const systemItem = { name: 'settings', label: '系统设置', to: '/settings', icon: Settings }

const showShell = computed(() => route.name !== 'login')
const pageTitle = computed(() => {
  if (route.name === 'dashboard') return '总览'
  if (route.name === 'discover') return '发现'
  if (route.name === 'comic') return '作品详情'
  if (route.name === 'subscriptions') return '订阅追更'
  if (route.name === 'downloads') return '下载任务'
  if (route.name === 'library') return '本地资料库'
  if (route.name === 'sources') return '漫画源'
  if (route.name === 'settings') return '系统设置'
  if (route.name === 'logs') return '系统日志'
  return 'Manco'
})

watch(
  () => route.fullPath,
  () => {
    menuOpen.value = false
  },
)

onMounted(() => {
  if (!auth.ready) {
    auth.load()
  }
})

async function signOut() {
  await auth.logout()
  router.push({ name: 'login' })
}
</script>

<template>
  <RouterView v-if="!showShell" />
  <div v-else class="app-shell">
    <aside class="sidebar" :class="{ open: menuOpen }">
      <div class="brand">
        <span class="brand-mark">M</span>
        <span class="brand-text">
          <strong>Manco</strong>
          <span>漫画订阅下载</span>
        </span>
      </div>
      <nav class="nav">
        <RouterLink v-for="item in navItems" :key="item.name" :to="item.to">
          <component :is="item.icon" :size="17" />
          <span>{{ item.label }}</span>
        </RouterLink>
      </nav>
      <div class="sidebar-foot">
        <div class="user-chip">
          <User :size="15" />
          <span>{{ auth.user?.username || '未登录' }}</span>
        </div>
        <button class="btn secondary small" type="button" @click="signOut">
          <LogOut :size="15" />
          退出登录
        </button>
        <nav class="nav sidebar-settings">
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
          <div>
            <h1>{{ pageTitle }}</h1>
            <p>订阅漫画源，按话自动打包为 CBZ</p>
          </div>
        </div>
      </header>
      <RouterView />
    </main>
  </div>
</template>
