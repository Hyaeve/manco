import { reactive } from 'vue'

export const notices = reactive([])
let seed = 0

export function notify(message, type = 'info', options = {}) {
  const text = String(message || '').trim()
  if (!text) return 0
  if (type && typeof type === 'object') {
    options = type
    type = 'info'
  }
  if (type === true) type = 'error'
  if (type === false || !type) type = 'info'
  const id = ++seed
  const level = ['success', 'error', 'warning', 'info'].includes(type) ? type : 'info'
  notices.push({ id, message: text, type: level, error: level === 'error', action: options.action || null })
  const timeout = Number(options.timeout) || (level === 'error' ? 7000 : level === 'warning' ? 5600 : 4200)
  window.setTimeout(() => dismissNotice(id), timeout)
  return id
}

export function dismissNotice(id) {
  const index = notices.findIndex((item) => item.id === id)
  if (index >= 0) notices.splice(index, 1)
}

export function clearNotices() {
  notices.splice(0, notices.length)
}
