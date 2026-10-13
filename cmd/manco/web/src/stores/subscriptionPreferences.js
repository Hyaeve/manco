const DOWNLOAD_DIR_KEY = 'manco.subscription-download-dir.v1'

export function lastSubscriptionDownloadDir() {
  if (typeof window === 'undefined') return ''
  try {
    return window.localStorage.getItem(DOWNLOAD_DIR_KEY) || ''
  } catch {
    return ''
  }
}

export function rememberSubscriptionDownloadDir(value) {
  if (typeof window === 'undefined') return
  const path = String(value || '').trim()
  try {
    if (path) window.localStorage.setItem(DOWNLOAD_DIR_KEY, path)
    else window.localStorage.removeItem(DOWNLOAD_DIR_KEY)
  } catch {
    // Ignore storage restrictions; subscription creation must still work.
  }
}
