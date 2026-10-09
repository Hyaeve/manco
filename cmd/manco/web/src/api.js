const jsonHeaders = { 'Content-Type': 'application/json' }

async function request(path, options = {}) {
  const response = await fetch(path, {
    credentials: 'same-origin',
    headers: { ...(options.body ? jsonHeaders : {}), ...(options.headers || {}) },
    ...options,
  })
  const text = await response.text()
  let payload = null
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch {
      payload = { error: text }
    }
  }
  if (!response.ok) {
    const error = new Error(payload?.error || `请求失败（${response.status}）`)
    error.status = response.status
    throw error
  }
  return payload
}

export const api = {
  login: (username, password) =>
    request('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }),
  setup: () => request('/api/auth/setup'),
  register: (payload) => request('/api/auth/register', { method: 'POST', body: JSON.stringify(payload) }),
  logout: () => request('/api/auth/logout', { method: 'POST' }),
  me: () => request('/api/auth/me'),

  sources: () => request('/api/sources'),
  updateSource: (id, payload) =>
    request(`/api/sources/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) }),
  saveAccount: (id, payload) =>
    request(`/api/sources/${encodeURIComponent(id)}/account`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteAccount: (id) => request(`/api/sources/${encodeURIComponent(id)}/account`, { method: 'DELETE' }),
  search: (id, query, page = 1) =>
    request(`/api/sources/${encodeURIComponent(id)}/search?q=${encodeURIComponent(query)}&page=${page}`),
  browse: (id, filters = {}, page = 1) => {
    const params = new URLSearchParams({ page: String(page) })
    for (const [key, value] of Object.entries(filters)) {
      if (value !== undefined && value !== null && String(value) !== '') {
        params.set(key, String(value))
      }
    }
    return request(`/api/sources/${encodeURIComponent(id)}/browse?${params.toString()}`)
  },
  comic: (id, comicId) =>
    request(`/api/sources/${encodeURIComponent(id)}/comics/${encodeURIComponent(comicId)}`),

  subscriptions: () => request('/api/subscriptions'),
  createSubscription: (payload) => request('/api/subscriptions', { method: 'POST', body: JSON.stringify(payload) }),
  updateSubscription: (id, payload) =>
    request(`/api/subscriptions/${id}`, { method: 'PATCH', body: JSON.stringify(payload) }),
  deleteSubscription: (id) => request(`/api/subscriptions/${id}`, { method: 'DELETE' }),
  checkSubscription: (id) => request(`/api/subscriptions/${id}/check`, { method: 'POST' }),
  downloadSubscription: (id) => request(`/api/subscriptions/${id}/download`, { method: 'POST' }),

  downloads: (limit = 200) => request(`/api/downloads?limit=${limit}`),
  createDownload: (payload) => request('/api/downloads', { method: 'POST', body: JSON.stringify(payload) }),
  retryDownload: (id) => request(`/api/downloads/${id}/retry`, { method: 'POST' }),
  retryDownloads: async (ids) => {
    const results = []
    for (const id of ids) {
      results.push(await request(`/api/downloads/${id}/retry`, { method: 'POST' }))
    }
    return results
  },
  deleteDownload: (id, removeFile = false) =>
    request(`/api/downloads/${id}?removeFile=${removeFile}`, { method: 'DELETE' }),

  library: () => request('/api/library'),
  settings: () => request('/api/settings'),
  saveSettings: (payload) => request('/api/settings', { method: 'PUT', body: JSON.stringify(payload) }),
  stats: () => request('/api/stats'),
  logs: (limit = 300) => request(`/api/logs?limit=${limit}`),

  imageUrl: (url, sourceId, referer) => {
    if (!url) return ''
    const params = new URLSearchParams({ url })
    if (sourceId) params.set('sourceId', sourceId)
    if (referer) params.set('referer', referer)
    return `/api/proxy/image?${params.toString()}`
  },
}
