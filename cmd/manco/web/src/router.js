import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from './stores/auth'

const routes = [
  { path: '/login', name: 'login', component: () => import('./views/LoginView.vue'), meta: { public: true } },
  { path: '/', name: 'dashboard', component: () => import('./views/DashboardView.vue') },
  { path: '/discover', name: 'discover', component: () => import('./views/DiscoverView.vue') },
  {
    path: '/comic/:sourceId/:comicId',
    name: 'comic',
    component: () => import('./views/ComicView.vue'),
  },
  { path: '/subscriptions', name: 'subscriptions', component: () => import('./views/SubscriptionsView.vue') },
  { path: '/downloads', name: 'downloads', component: () => import('./views/DownloadsView.vue') },
  { path: '/library', name: 'library', component: () => import('./views/LibraryView.vue') },
  { path: '/local', name: 'local', component: () => import('./views/LocalLibraryView.vue') },
  { path: '/sources', name: 'sources', component: () => import('./views/SourcesView.vue') },
  { path: '/settings', name: 'settings', component: () => import('./views/SettingsView.vue') },
  { path: '/logs', name: 'logs', component: () => import('./views/LogsView.vue') },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!auth.ready) {
    await auth.load()
  }
  if (!to.meta.public && !auth.isAuthenticated) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.name === 'login' && auth.isAuthenticated) {
    return { name: 'dashboard' }
  }
  return true
})
