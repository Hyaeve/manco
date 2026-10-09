const NAV_KEY = 'manco.discover.nav.v1'

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
