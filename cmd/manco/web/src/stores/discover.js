const NAV_KEY = 'manco.discover.nav.v1'
const DEFAULT_NAV = { kind: 'bangumi', source: 'bangumi-recent', page: 1, scrollTop: 0 }

// 探索发现数据缓存：SPA 会话内保留各来源已加载的作品列表，重新进入
// 该页面时直接复用，避免每次都重新拉取远程内容源。缓存过期后才清空。
const PANEL_CACHE_TTL = 30 * 60 * 1000
const panels = {}
let panelsLoadedAt = 0
let navState = null

export function readDiscoverPanels() {
	return panels
}

export function markDiscoverPanelsLoaded() {
	panelsLoadedAt = Date.now()
}

export function discoverPanelsExpired() {
	return panelsLoadedAt === 0 || Date.now() - panelsLoadedAt > PANEL_CACHE_TTL
}

export function resetDiscoverPanels() {
	for (const key of Object.keys(panels)) delete panels[key]
	panelsLoadedAt = 0
}

export function resetDiscoverSession() {
  navState = null
  resetDiscoverPanels()
  try {
    window.sessionStorage.removeItem(NAV_KEY)
  } catch {}
}

// 记录发现页的浏览位置，便于从作品详情返回时恢复到原来的栏目、页码与滚动位置。
export function readDiscoverNav() {
  if (navState) return { ...navState }
  try {
    const value = JSON.parse(window.sessionStorage.getItem(NAV_KEY) || '{}')
    navState = { ...DEFAULT_NAV, ...(value && typeof value === 'object' && !Array.isArray(value) ? value : {}) }
    return { ...navState }
  } catch {
    navState = { ...DEFAULT_NAV }
    return { ...navState }
  }
}

export function saveDiscoverNav(state) {
  navState = { ...readDiscoverNav(), ...state }
  try {
    window.sessionStorage.setItem(NAV_KEY, JSON.stringify(navState))
  } catch {}
  return { ...navState }
}
