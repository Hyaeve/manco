export const SOURCES_CHANGED_EVENT = 'manco:sources-changed'

export function emitSourcesChanged() {
  if (typeof window === 'undefined') return
  window.dispatchEvent(new CustomEvent(SOURCES_CHANGED_EVENT))
}
