import { reactive } from 'vue'

export const confirmState = reactive({
  open: false,
  title: '',
  message: '',
  confirmText: '确认',
  cancelText: '取消',
  danger: false,
  resolver: null,
})

export function askConfirm(options = {}) {
  if (confirmState.resolver) {
    confirmState.resolver(false)
  }
  confirmState.open = true
  confirmState.title = options.title || '请确认'
  confirmState.message = options.message || ''
  confirmState.confirmText = options.confirmText || '确认'
  confirmState.cancelText = options.cancelText || '取消'
  confirmState.danger = Boolean(options.danger)
  return new Promise((resolve) => {
    confirmState.resolver = resolve
  })
}

export function resolveConfirm(value) {
  const resolver = confirmState.resolver
  confirmState.open = false
  confirmState.resolver = null
  if (resolver) resolver(Boolean(value))
}
