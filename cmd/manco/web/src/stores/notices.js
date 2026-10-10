import { reactive } from 'vue'

export const notices = reactive([])
let seed = 0

export function notify(message, error = false, options = {}) {
  const text = String(message || '').trim()
  if (!text) return 0
  const id = ++seed
  notices.push({ id, message: text, error: Boolean(error), action: options.action || null })
  const timeout = Number(options.timeout) || (error ? 7000 : 4200)
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
