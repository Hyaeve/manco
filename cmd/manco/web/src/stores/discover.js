const NAV_KEY = 'manco.discover.nav.v1'

// 探索发现数据缓存：SPA 会话内保留各来源已加载的作品列表，重新进入
// 该页面时直接复用，避免每次都重新拉取远程内容源。缓存过期后才清空。
const PANEL_CACHE_TTL = 30 * 60 * 1000
const panels = {}
let panelsLoadedAt = 0

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

// 记录发现页的浏览位置，便于从作品详情返回时恢复到原来的栏目、页码与滚动位置。
export function readDiscoverNav() {
  try {
    const value = JSON.parse(window.sessionStorage.getItem(NAV_KEY) || '{}')
    return value && typeof value === 'object' ? value : {}
  } catch {
    return {}
  }
}

export function saveDiscoverNav(state) {
  try {
    const previous = readDiscoverNav()
    const next = { ...previous, ...state }
    window.sessionStorage.setItem(NAV_KEY, JSON.stringify(next))
    return next
  } catch {
    return {}
  }
}
