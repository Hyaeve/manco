export function openExternalUrl(url) {
  const target = String(url || '').trim()
  if (!target) return false
  const opened = window.open(target, '_blank', 'noopener,noreferrer')
  if (opened) opened.opener = null
  return true
}
