const jsonHeaders = { 'Content-Type': 'application/json' }
const anonymousPaths = new Set(['/api/auth/login', '/api/auth/setup', '/api/auth/register'])

async function request(path, options = {}) {
  const isFormData = typeof FormData !== 'undefined' && options.body instanceof FormData
  const headers = { ...(options.body && !isFormData ? jsonHeaders : {}), ...(options.headers || {}) }
  const response = await fetch(path, {
    credentials: 'same-origin',
    headers,
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
    if (response.status === 401 && !anonymousPaths.has(path)) {
      window.dispatchEvent(new CustomEvent('manco:unauthorized', { detail: { path } }))
    }
    throw error
  }
  return payload
}

export const api = {
  login: (username, password) =>
    request('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }),
  setup: () => request('/api/auth/setup'),
  loginBangumi: () => request('/api/login/bangumi'),
  loginSubscriptions: () => request('/api/login/subscriptions'),
  register: (payload) => request('/api/auth/register', { method: 'POST', body: JSON.stringify(payload) }),
  logout: () => request('/api/auth/logout', { method: 'POST' }),
  me: () => request('/api/auth/me'),

  sources: () => request('/api/sources'),
  sourceRepo: () => request('/api/source-repo'),
  repositories: () => request('/api/repositories'),
  createRepository: (payload) =>
    request('/api/repositories', { method: 'POST', body: JSON.stringify(payload) }),
  updateRepository: (id, payload) =>
    request(`/api/repositories/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(payload) }),
  syncRepository: (id) =>
    request(`/api/repositories/${encodeURIComponent(id)}/sync`, { method: 'POST' }),
  deleteRepository: (id) =>
    request(`/api/repositories/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  importRepositoryExtension: (repositoryId, extensionId) =>
    request(`/api/repositories/${encodeURIComponent(repositoryId)}/extensions/${encodeURIComponent(extensionId)}`, { method: 'POST' }),
  prunePlugins: () => request('/api/plugins/prune', { method: 'POST' }),
  installAPK: (file) => {
    const form = new FormData()
    form.append('file', file)
    return request('/api/plugins/install-apk', { method: 'POST', body: form })
  },
  createCustomSource: (payload) =>
    request('/api/custom-sources', { method: 'POST', body: JSON.stringify(payload) }),
  customSourceConfig: (id) => request(`/api/custom-sources/${encodeURIComponent(id)}`),
  updateCustomSource: (id, payload) =>
    request(`/api/custom-sources/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(payload) }),
  renameCustomSource: (id, name) =>
    request(`/api/custom-sources/${encodeURIComponent(id)}/name`, { method: 'PUT', body: JSON.stringify({ name }) }),
  deleteCustomSource: (id) =>
    request(`/api/custom-sources/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  updateSource: (id, payload) =>
    request(`/api/sources/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) }),
  updateSourceOrder: (payload) =>
    request('/api/sources/order', { method: 'PUT', body: JSON.stringify(payload) }),
  sourceIcons: () => request('/api/icons'),
  uploadIcon: (file) => {
    const form = new FormData()
    form.append('file', file)
    return request('/api/icons', { method: 'POST', body: form })
  },
  downloadIcon: (url, id = '') =>
    request('/api/icons/download', { method: 'POST', body: JSON.stringify({ url, id }) }),
  updateSourceIcon: (id, payload) =>
    request(`/api/sources/${encodeURIComponent(id)}/icon`, { method: 'POST', body: JSON.stringify(payload) }),
  saveAccount: (id, payload) =>
    request(`/api/sources/${encodeURIComponent(id)}/account`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteAccount: (id) => request(`/api/sources/${encodeURIComponent(id)}/account`, { method: 'DELETE' }),
  sourceBrowserSession: (id) =>
    request(`/api/sources/${encodeURIComponent(id)}/browser-session`),
  importSourceCookies: (id, payload) =>
    request(`/api/sources/${encodeURIComponent(id)}/cookies`, { method: 'POST', body: JSON.stringify(payload) }),
  pluginAuthCapabilities: (id) =>
    request(`/api/sources/${encodeURIComponent(id)}/plugin-auth`),
  pluginCredentialLogin: (id, payload) =>
    request(`/api/sources/${encodeURIComponent(id)}/plugin-auth/login`, { method: 'POST', body: JSON.stringify(payload) }),
  pluginPreferences: (id) =>
    request(`/api/sources/${encodeURIComponent(id)}/plugin-preferences`),
  updatePluginPreference: (id, payload) =>
    request(`/api/sources/${encodeURIComponent(id)}/plugin-preferences`, { method: 'PUT', body: JSON.stringify(payload) }),
  search: (id, query, page = 1, signal) =>
    request(`/api/sources/${encodeURIComponent(id)}/search?q=${encodeURIComponent(query)}&page=${page}`, { signal }),
  globalSearch: (query, kind = '', page = 1) => {
    const params = new URLSearchParams({ q: query, page: String(page) })
    if (kind) params.set('kind', kind)
    return request(`/api/search?${params.toString()}`)
  },
  searchHistory: () => request('/api/search/history'),
  sourceFilters: (id) => request(`/api/sources/${encodeURIComponent(id)}/filters`),
  testSource: (id) => request(`/api/sources/${encodeURIComponent(id)}/test`, { method: 'POST' }),
  browse: (id, filters = {}, page = 1, signal) => {
    const params = new URLSearchParams({ page: String(page) })
    for (const [key, value] of Object.entries(filters)) {
      if (value !== undefined && value !== null && String(value) !== '') {
        params.set(key, String(value))
      }
    }
    return request(`/api/sources/${encodeURIComponent(id)}/browse?${params.toString()}`, { signal })
  },
  bangumiBrowse: (feed, page = 1, limit = 42, signal) => {
    const params = new URLSearchParams({
      feed: String(feed || ''),
      page: String(page),
      limit: String(limit),
    })
    return request(`/api/bangumi/discover?${params.toString()}`, { signal })
  },
  bangumiSearch: (query, page = 1, limit = 42, signal) => {
    const params = new URLSearchParams({
      q: String(query || ''),
      page: String(page),
      limit: String(limit),
    })
    return request(`/api/bangumi/search?${params.toString()}`, { signal })
  },
  comic: (id, comicId) =>
    request(`/api/sources/${encodeURIComponent(id)}/comics/${encodeURIComponent(comicId)}`),
  chapterPreview: (id, comicId, chapter) =>
    request(`/api/sources/${encodeURIComponent(id)}/comics/${encodeURIComponent(comicId)}/preview`, {
      method: 'POST',
      body: JSON.stringify({ chapter }),
    }),
  chapterPreviewFileUrl: (id, comicId, key, index) => {
    if (!id || !comicId || !key || index < 0) return ''
    const params = new URLSearchParams({ key, index: String(index) })
    return `/api/sources/${encodeURIComponent(id)}/comics/${encodeURIComponent(comicId)}/preview/file?${params.toString()}`
  },
  comicDownloads: (id, comicId) =>
    request(`/api/sources/${encodeURIComponent(id)}/comics/${encodeURIComponent(comicId)}/downloads`),

  subscriptions: (limit = 0, offset = 0, options = {}) => {
    const params = new URLSearchParams()
    if (limit) params.set('limit', String(limit))
    if (offset) params.set('offset', String(offset))
    if (options.kind && options.kind !== 'all') params.set('kind', options.kind)
    if (options.dir) params.set('dir', options.dir)
    if (options.sort) params.set('sort', options.sort)
    if (typeof options.desc === 'boolean') params.set('desc', options.desc ? '1' : '0')
    const query = params.toString()
    return request(`/api/subscriptions${query ? `?${query}` : ''}`)
  },
  archivedSubscriptions: () => request('/api/subscriptions/archived'),
  matchSubscription: (sourceId, comicId) => {
    const params = new URLSearchParams({ sourceId, comicId })
    return request(`/api/subscriptions/match?${params.toString()}`)
  },
  createSubscription: (payload) => request('/api/subscriptions', { method: 'POST', body: JSON.stringify(payload) }),
  updateSubscription: (id, payload) =>
    request(`/api/subscriptions/${id}`, { method: 'PATCH', body: JSON.stringify(payload) }),
  deleteSubscription: (id) => request(`/api/subscriptions/${id}`, { method: 'DELETE' }),
  checkSubscription: (id) => request(`/api/subscriptions/${id}/check`, { method: 'POST' }),
  downloadSubscription: (id) => request(`/api/subscriptions/${id}/download`, { method: 'POST' }),
  downloadAllSubscription: (id) => request(`/api/subscriptions/${id}/download-all`, { method: 'POST' }),
  fillSubscription: (id) => request(`/api/subscriptions/${id}/fill`, { method: 'POST' }),
  scrapeSubscription: (id) => request(`/api/subscriptions/${id}/scrape`, { method: 'POST' }),
  archiveSubscription: (id) => request(`/api/subscriptions/${id}/archive`, { method: 'POST' }),
  restoreSubscription: (id) => request(`/api/subscriptions/${id}/restore`, { method: 'POST' }),
  backfillChapterMarkers: (id) => request(`/api/subscriptions/${id}/chapter-marker/backfill`, { method: 'POST' }),

  downloads: (limit = 0, offset = 0, status = '', options = {}) => {
    const params = new URLSearchParams()
    if (limit) params.set('limit', String(limit))
    if (offset) params.set('offset', String(offset))
    if (status) params.set('status', status)
    if (options.filter) params.set('filter', options.filter)
    if (options.query) params.set('q', options.query)
    if (options.compact) params.set('compact', '1')
    if (options.ids?.length) params.set('ids', options.ids.join(','))
    const query = params.toString()
    return request(`/api/downloads${query ? `?${query}` : ''}`, { signal: options.signal })
  },
  createDownload: (payload) => request('/api/downloads', { method: 'POST', body: JSON.stringify(payload) }),
  retryDownload: (id) => request(`/api/downloads/${id}/retry`, { method: 'POST' }),
  retryDownloads: (ids) => request('/api/downloads/retry', {
    method: 'POST',
    body: JSON.stringify({ ids }),
  }),
  deleteDownload: (id, removeFile = false) =>
    request(`/api/downloads/${id}?removeFile=${removeFile}`, { method: 'DELETE' }),

  library: () => request('/api/library'),
  localLibrary: (limit = 0, offset = 0, summary = false, options = {}) => {
    const params = new URLSearchParams()
    if (limit) params.set('limit', String(limit))
    if (offset) params.set('offset', String(offset))
    if (summary) params.set('summary', '1')
    if (options.query) params.set('q', options.query)
    if (options.kind && options.kind !== 'all') params.set('kind', options.kind)
    if (options.root) params.set('root', options.root)
    if (options.sort) params.set('sort', options.sort)
    if (options.desc) params.set('desc', '1')
    if (options.hasCover) params.set('hasCover', '1')
    const query = params.toString()
    return request(`/api/local${query ? `?${query}` : ''}`, { signal: options.signal })
  },
  localItem: (path) => request(`/api/local/item?${new URLSearchParams({ path })}`),
  localChapterThumbnailUrl: (file) =>
    `/api/local/cbz/thumbnail?${new URLSearchParams({ path: file.path, w: '240', v: file.modTime || '' })}`,
  localFileUrl: (path, download = false, width = 0) => {
    if (!path) return ''
    const params = new URLSearchParams({ path })
    if (download) params.set('download', '1')
    if (width) params.set('w', String(width))
    return `/api/local/file?${params.toString()}`
  },
  localCBZ: (path) => request(`/api/local/cbz?path=${encodeURIComponent(path)}`),
  localEPUB: (path) => request(`/api/local/epub?path=${encodeURIComponent(path)}`),
  localMerge: (paths) => request('/api/local/merge', { method: 'POST', body: JSON.stringify({ paths }) }),
  localSimplify: (paths) => request('/api/local/simplify', { method: 'POST', body: JSON.stringify({ paths }) }),
  localDelete: (paths) => request('/api/local/delete', { method: 'POST', body: JSON.stringify({ paths }) }),
  localCBZFileUrl: (path, entry, download = false) => {
    if (!path || !entry) return ''
    const params = new URLSearchParams({ path, entry })
    if (download) params.set('download', '1')
    return `/api/local/cbz/file?${params.toString()}`
  },
  localCBZDocument: (path, entry) =>
    request(`/api/local/cbz/document?path=${encodeURIComponent(path)}&entry=${encodeURIComponent(entry)}`),
  localEPUBFileUrl: (path, entry, download = false) => {
    if (!path || !entry) return ''
    const params = new URLSearchParams({ path, entry })
    if (download) params.set('download', '1')
    return `/api/local/epub/file?${params.toString()}`
  },
  settings: () => request('/api/settings'),
  saveSettings: (payload) => request('/api/settings', { method: 'PUT', body: JSON.stringify(payload) }),
  version: () => request('/api/version'),
  checkUpdate: () => request('/api/update-check'),
  stats: () => request('/api/stats'),
  dashboardUpcoming: (limit = 3) => request(`/api/dashboard/upcoming?limit=${limit}`),
  logs: (limit = 300) => request(`/api/logs?limit=${limit}`),
  activity: () => request('/api/activity'),
  downloadDirectories: (path = '') => {
    const params = path ? `?path=${encodeURIComponent(path)}` : ''
    return request(`/api/download-directories${params}`)
  },
  createDownloadDirectory: (parent, name) =>
    request('/api/download-directories', { method: 'POST', body: JSON.stringify({ parent, name }) }),
  seriesDirName: (title, convertToSimplified) =>
    request('/api/tools/series-dir-name', {
      method: 'POST',
      body: JSON.stringify({ title, convertToSimplified: Boolean(convertToSimplified) }),
    }),

  imageUrl: (url, sourceId, referer, width = 0) => {
    if (!url) return ''
    const params = new URLSearchParams({ url })
    if (sourceId) params.set('sourceId', sourceId)
    if (referer) params.set('referer', referer)
    if (width) params.set('w', String(width))
    return `/api/proxy/image?${params.toString()}`
  },
  cachedCoverUrl: (url, width = 320) => {
    if (!url || !width) return url || ''
    return `${url}${url.includes('?') ? '&' : '?'}w=${encodeURIComponent(width)}`
  },
}
