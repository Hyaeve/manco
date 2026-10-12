<script setup>
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import {
  Boxes,
  Check,
  ChevronDown,
  Eye,
  EyeOff,
  KeyRound,
  Loader2,
  MoreVertical,
  Search,
  PackagePlus,
  Plus,
  RefreshCw,
  Pencil,
  Save,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  Trash2,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import { askConfirm } from '../stores/confirm'
import { notify } from '../stores/notices'
import PasswordInput from '../components/PasswordInput.vue'
import RoundedSelect from '../components/RoundedSelect.vue'
import SitePicker from '../components/SitePicker.vue'

const sources = ref([])
const repositories = ref([])
const accounts = ref({})
const defaults = ref({ maxChapterConcurrency: 1, maxPageConcurrency: 4 })
const loading = ref(true)
const busy = ref('')
const activeKind = ref('comic')
const repositoryOpen = ref(false)
const addSourceOpen = ref(false)
const addRepositoryID = ref('')
const sourceSearch = ref('')
const categoryOpen = ref(false)
const categorySource = ref(null)
const categorySelection = ref({})
const editorOpen = ref(false)
const editorSource = ref(null)
const expandedRepositories = ref({})
const forms = reactive({})
const repositoryForm = reactive({ name: '', url: '', kind: 'json' })
const editingRepositoryId = ref('')
const dragId = ref('')
const browserOpen = ref(false)
const browserSource = ref(null)
const browserSession = reactive({ url: '', userAgent: '' })
const browserCookies = ref('')
const sourceSelection = ref(new Set())
const sourceAnchor = ref(-1)
const extensionCandidates = ref([])
const candidateFilter = ref('all')
const candidateLoading = ref(false)
const candidateLoadedRepos = ref(new Set())
const pluginAuth = ref(null)
const pluginPreferences = ref(null)
const pluginPreferenceValues = ref({})
const pluginAuthLoading = ref(false)
const pluginPrefLoading = ref(false)
const pluginLoginBusy = ref(false)

const repositoryKinds = [
  { value: 'json', label: 'JSON 清单' },
  { value: 'jar', label: 'JAR 拓展' },
  { value: 'mihon', label: 'Mihon' },
  { value: 'aniyomi', label: 'Aniyomi' },
  { value: 'ireader', label: 'iReader' },
  { value: 'cloudstream', label: 'CloudStream' },
  { value: 'tsundoku', label: 'Tsundoku' },
]

const comicSources = computed(() => sources.value.filter((item) => (item.kind || 'comic') === 'comic'))
const bookSources = computed(() => sources.value.filter((item) => item.kind === 'book'))
const otherSources = computed(() => sources.value.filter((item) => item.kind === 'other'))
const visibleSources = computed(() => {
  if (activeKind.value === 'book') return bookSources.value
  if (activeKind.value === 'other') return otherSources.value
  return comicSources.value
})
const repositoryOptions = computed(() =>
  repositories.value.map((repository) => ({
    value: repository.id,
    label: `${repository.name} · ${repository.kind || 'json'}`,
  })),
)
const selectedRepository = computed(() => repositories.value.find((item) => item.id === addRepositoryID.value) || null)
const selectedExtensions = computed(() => {
  const keyword = sourceSearch.value.trim().toLowerCase()
  const rows = selectedRepository.value?.extensions || []
  const kind = activeKind.value
  return rows.filter((item) => {
    if (kind !== 'repository' && (item.kind || 'comic') !== kind) return false
    if (!keyword) return true
    return `${item.name} ${item.packageName || ''} ${item.description || ''}`.toLowerCase().includes(keyword)
  })
})

const candidateCategories = computed(() => {
  const values = new Set()
  for (const item of extensionCandidates.value) {
    const value = String(item.contentType || item.kind || '').trim()
    if (value) values.add(value)
  }
  return ['all', ...Array.from(values).sort()]
})

function candidateCategoryLabel(value) {
  return {
    all: '全部分类',
    MANGA: '漫画',
    MANHWA: '韩漫',
    MANHUA: '国漫',
    HENTAI_MANGA: '成人漫画',
    COMICS: '美漫',
    NOVEL: '小说',
    HENTAI_NOVEL: '成人小说',
    ONE_SHOT: '短篇',
    DOUJINSHI: '同人',
    IMAGE_SET: '图集',
    ARTIST_CG: '画师 CG',
    GAME_CG: '游戏 CG',
    VIDEO: '视频',
    OTHER: '其他',
  }[value] || value || '其他'
}

const candidateFilterOptions = computed(() =>
  candidateCategories.value.map((value) => ({ value, label: candidateCategoryLabel(value) })),
)

const candidateCategoryIcon = (value) => value || 'OTHER'

const visibleCandidates = computed(() => {
  const keyword = sourceSearch.value.trim().toLowerCase()
  return extensionCandidates.value.filter((item) => {
    if (activeKind.value !== 'repository' && (item.kind || 'comic') !== activeKind.value) return false
    if (candidateFilter.value !== 'all' && String(item.contentType || item.kind) !== candidateFilter.value) return false
    if (!keyword) return true
    return `${item.name} ${item.sourceName || ''} ${item.description || ''}`.toLowerCase().includes(keyword)
  })
})

const selectableVisibleSources = computed(() => visibleSources.value.filter((item) => !item.builtin))
const selectedSources = computed(() => sources.value.filter((item) => sourceSelection.value.has(item.id)))
const allVisibleSourcesSelected = computed(
  () => selectableVisibleSources.value.length > 0 && selectableVisibleSources.value.every((item) => sourceSelection.value.has(item.id)),
)

onMounted(load)

onMounted(() => window.addEventListener('keydown', onSourceKeydown))
onUnmounted(() => window.removeEventListener('keydown', onSourceKeydown))

function onSourceKeydown(event) {
  if (event.key === 'Escape' && sourceSelection.value.size) clearSourceSelection()
}

watch(addRepositoryID, () => {
  extensionCandidates.value = []
  candidateFilter.value = 'all'
})

watch(selectedRepository, (repository) => {
  if (addSourceOpen.value) loadRepositoryCandidates(repository)
})

watch(activeKind, () => {
  if (addSourceOpen.value) loadRepositoryCandidates(selectedRepository.value)
})

watch(activeKind, () => clearSourceSelection())

async function load() {
  loading.value = true
  try {
    const [sourcePayload, repositoryPayload, settingsPayload] = await Promise.all([
      api.sources(),
      api.repositories(),
      api.settings().catch(() => ({})),
    ])
    sources.value = sourcePayload.items || []
    accounts.value = sourcePayload.accounts || {}
    repositories.value = repositoryPayload.items || []
    defaults.value = {
      maxChapterConcurrency: Number(settingsPayload.maxChapterConcurrency) || 1,
      maxPageConcurrency: Number(settingsPayload.maxPageConcurrency) || 4,
    }
    if (!addRepositoryID.value || !repositories.value.some((item) => item.id === addRepositoryID.value)) {
      addRepositoryID.value = repositories.value[0]?.id || ''
    }
    for (const item of sources.value) {
      const account = accounts.value[item.id] || {}
      const settings = account.settings || {}
      if (!forms[item.id]) {
        forms[item.id] = reactive({
          username: account.username || '',
          password: account.password || '',
          cookie: '',
          homeUrl: account.homeUrl || '',
          chapterConcurrency: Number(settings.chapterConcurrency || defaults.value.maxChapterConcurrency || 1),
          pageConcurrency: Number(settings.pageConcurrency || defaults.value.maxPageConcurrency || 4),
          batchSize: Number(settings.batchSize || 0),
          batchIntervalMinutes: Number(settings.batchIntervalMinutes || 0),
        })
      } else {
        forms[item.id].username = account.username || ''
        if (account.password) forms[item.id].password = account.password
        forms[item.id].homeUrl = account.homeUrl || ''
        forms[item.id].chapterConcurrency = Number(settings.chapterConcurrency || defaults.value.maxChapterConcurrency || 1)
        forms[item.id].pageConcurrency = Number(settings.pageConcurrency || defaults.value.maxPageConcurrency || 4)
        forms[item.id].batchSize = Number(settings.batchSize || 0)
        forms[item.id].batchIntervalMinutes = Number(settings.batchIntervalMinutes || 0)
      }
    }
  } catch (err) {
    notify(`资源仓库加载失败：${err.message}`, 'error')
  } finally {
    loading.value = false
  }
}

function accountOf(id) {
  return accounts.value[id] || null
}

function isSourceSelected(item) {
  return sourceSelection.value.has(item.id)
}

function enterSourceSelection(item, index, event) {
  if (item.builtin) return
  event?.preventDefault?.()
  const next = new Set(sourceSelection.value)
  if (event?.shiftKey && sourceAnchor.value >= 0) {
    const start = Math.min(sourceAnchor.value, index)
    const end = Math.max(sourceAnchor.value, index)
    for (let cursor = start; cursor <= end; cursor += 1) {
      const row = visibleSources.value[cursor]
      if (row && !row.builtin) next.add(row.id)
    }
  } else {
    next.add(item.id)
  }
  sourceSelection.value = next
  sourceAnchor.value = index
}

function toggleSourceSelection(item, index, event) {
  if (item.builtin) {
    openEditor(item)
    return
  }
  if (!sourceSelection.value.size) {
    enterSourceSelection(item, index, event)
    return
  }
  enterSourceSelection(item, index, event?.shiftKey ? event : {})
}

function selectAllSources() {
  sourceSelection.value = new Set(selectableVisibleSources.value.map((item) => item.id))
  sourceAnchor.value = selectableVisibleSources.value.length ? 0 : -1
}

function clearSourceSelection() {
  sourceSelection.value = new Set()
  sourceAnchor.value = -1
}

async function removeSelectedSources() {
  const rows = selectedSources.value.filter((item) => !item.builtin)
  if (!rows.length) return
  const confirmed = await askConfirm({
    title: '卸载内容源',
    message: `确定从当前资源库卸载 ${rows.length} 个内容源吗？之后仍可从拓展仓库重新添加。`,
    confirmText: '卸载',
    danger: true,
  })
  if (!confirmed) return
  busy.value = 'source-batch-delete'
  try {
    for (const item of rows) await api.deleteCustomSource(item.id)
    notify(`已卸载 ${rows.length} 个内容源`, 'success')
    clearSourceSelection()
    await load()
  } catch (err) {
    notify(`卸载失败：${err.message}`, 'error')
  } finally {
    busy.value = ''
  }
}

function connected(id) {
  const account = accountOf(id)
  return Boolean(account && (account.hasToken || account.hasCookie))
}

function sourceStatus(item) {
  if (item.hidden) return { label: '已停用', className: 'warning' }
  if (connected(item.id)) return { label: '已连接', className: 'success' }
  if (item.needsLogin) return { label: '未连接', className: '' }
  return { label: '可用', className: 'primary' }
}

function kindLabel(kind) {
  return { comic: '漫画', book: '书籍', other: '其他' }[kind] || '资源'
}

function categoryFilters(item) {
  return (item.filters || []).filter((group) => group.key !== 'sort' && group.key !== 'ranking')
}

function groupValues(group) {
  return (group.options || []).map((option) => String(option.value)).filter(Boolean)
}

function groupAllSelected(group) {
  const values = groupValues(group)
  return values.length > 0 && values.every((value) => categorySelection.value[group.key]?.includes(value))
}

function openCategory(item) {
  const groups = categoryFilters(item)
  if (!groups.length) {
    notify(`${item.name} 暂无可配置分类`, 'info')
    return
  }
  const selection = {}
  for (const group of groups) {
    selection[group.key] = (group.options || [])
      .filter((option) => option.value && !option.disabled)
      .map((option) => String(option.value))
  }
  categorySource.value = item
  categorySelection.value = selection
  categoryOpen.value = true
}

function toggleGroupAll(group) {
  const values = groupValues(group)
  const current = new Set(categorySelection.value[group.key] || [])
  if (values.every((value) => current.has(value))) {
    categorySelection.value = { ...categorySelection.value, [group.key]: [] }
    return
  }
  categorySelection.value = { ...categorySelection.value, [group.key]: values }
}

function toggleCategory(group, value) {
  const key = String(value)
  const current = categorySelection.value[group.key] || []
  categorySelection.value = {
    ...categorySelection.value,
    [group.key]: current.includes(key) ? current.filter((item) => item !== key) : [...current, key],
  }
}

async function saveCategories() {
  const item = categorySource.value
  if (!item) return
  busy.value = `category-${item.id}`
  try {
    const groups = categoryFilters(item)
    const allValues = groups.flatMap(groupValues)
    const selected = new Set(Object.values(categorySelection.value).flat())
    const disabledCategories = allValues.filter((value) => !selected.has(value))
    const result = await api.updateSource(item.id, { disabledCategories })
    const disabled = new Set(result.disabledCategories || [])
    item.filters = (item.filters || []).map((group) => ({
      ...group,
      options: (group.options || []).map((option) => ({ ...option, disabled: disabled.has(String(option.value)) })),
    }))
    categoryOpen.value = false
    notify(`${item.name} 的分类显示设置已保存`, 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = ''
  }
}

function isPluginSource(item) {
  return Boolean(item?.pluginSource)
}

function parseBrowserCookies(raw) {
  const text = String(raw || '')
  const rows = []
  for (let line of text.split(/\r?\n/)) {
    line = line.trim()
    if (!line) continue
    if (/^cookie:/i.test(line)) line = line.replace(/^cookie:/i, '').trim()
    if (line.includes('\t')) {
      const parts = line.split('\t')
      if (parts.length >= 2) line = `${parts[0]}=${parts[1]}`
    }
    if (!line.includes('=')) continue
    if (!/;\s*path=/i.test(line)) line += '; Path=/'
    rows.push(line)
  }
  return [...new Set(rows)]
}

async function openBrowserCheck(item) {
  busy.value = `browser-${item.id}`
  browserSource.value = item
  browserCookies.value = ''
  browserSession.url = ''
  browserSession.userAgent = ''
  try {
    const session = await api.sourceBrowserSession(item.id)
    browserSession.url = session.url || item.homepage || ''
    browserSession.userAgent = session.userAgent || ''
    browserOpen.value = true
  } catch (err) {
    notify(`${item.name}：${err.message}`, 'error')
  } finally {
    busy.value = ''
  }
}

async function importBrowserCookies() {
  const item = browserSource.value
  if (!item) return
  const cookies = parseBrowserCookies(browserCookies.value)
  if (!cookies.length) {
    notify('请粘贴至少一条 Cookie（每行一条）', 'warning')
    return
  }
  busy.value = `browser-${item.id}`
  try {
    const result = await api.importSourceCookies(item.id, {
      sourceName: '',
      url: browserSession.url || item.homepage || '',
      cookies,
    })
    notify(`${item.name} 已导入 ${result.imported || cookies.length} 条浏览器 Cookie`, 'success')
    browserOpen.value = false
  } catch (err) {
    notify(`${item.name}：${err.message}`, 'error')
  } finally {
    busy.value = ''
  }
}

function openEditor(item) {
  if (!forms[item.id]) return
  editorSource.value = item
  editorOpen.value = true
  loadPluginSettings(item)
}

async function loadPluginSettings(item) {
  pluginAuth.value = null
  pluginPreferences.value = null
  pluginPreferenceValues.value = {}
  if (!isPluginSource(item)) return
  pluginAuthLoading.value = true
  pluginPrefLoading.value = true
  try {
    const [authResult, prefResult] = await Promise.allSettled([
      api.pluginAuthCapabilities(item.id),
      api.pluginPreferences(item.id),
    ])
    if (authResult.status === 'fulfilled') pluginAuth.value = authResult.value
    if (prefResult.status === 'fulfilled') {
      pluginPreferences.value = prefResult.value
      const nodes = prefResult.value?.preferences?.screen?.nodes || prefResult.value?.preferences?.nodes || []
      const values = {}
      for (const node of nodes) {
        const raw = node.value
        if (raw && typeof raw === 'object' && 'value' in raw) values[node.id] = raw.value
        else if (raw && typeof raw === 'object' && 'values' in raw) values[node.id] = Array.from(raw.values || [])
      }
      pluginPreferenceValues.value = values
    }
  } finally {
    pluginAuthLoading.value = false
    pluginPrefLoading.value = false
  }
}

function pluginPreferenceValue(node) {
  const value = pluginPreferenceValues.value[node.id]
  return value === undefined ? '' : value
}

function setPluginPreferenceValue(node, value) {
  pluginPreferenceValues.value = { ...pluginPreferenceValues.value, [node.id]: value }
}

function pluginPreferenceNodes() {
  const payload = pluginPreferences.value || {}
  const screen = payload.preferences?.screen || payload.preferences?.update?.screen || payload.update?.screen || payload.screen || payload.preferences || {}
  return Array.isArray(screen.nodes) ? screen.nodes.filter((node) => node.visible !== false) : []
}

function applyPluginPreferenceScreen(payload) {
  pluginPreferences.value = payload
  const screen = payload?.preferences?.screen || payload?.preferences?.update?.screen || payload?.update?.screen || payload?.screen || payload?.preferences || {}
  const values = {}
  for (const node of screen.nodes || []) {
    const raw = node.value
    if (raw && typeof raw === 'object' && 'value' in raw) values[node.id] = raw.value
    else if (raw && typeof raw === 'object' && 'values' in raw) values[node.id] = Array.from(raw.values || [])
  }
  pluginPreferenceValues.value = { ...pluginPreferenceValues.value, ...values }
}

async function savePluginPreference(node) {
  const item = editorSource.value
  if (!item || !node?.id) return
  const payload = pluginPreferences.value || {}
  const screen = payload.preferences?.screen || payload.preferences?.update?.screen || payload.update?.screen || payload.screen || payload.preferences || {}
  const current = pluginPreferenceValue(node)
  let value
  if (node.kind === 'TOGGLE') value = { type: 'boolean', value: Boolean(current) }
  else if (node.kind === 'MULTI_CHOICE') value = { type: 'stringSet', values: Array.isArray(current) ? current : [] }
  else value = { type: 'string', value: String(current ?? '') }
  pluginPrefLoading.value = true
  try {
    const result = await api.updatePluginPreference(item.id, {
      revision: screen.revision || '',
      nodeId: node.id,
      value,
    })
    applyPluginPreferenceScreen(result)
    notify(`${item.name}：${node.title || '设置'}已保存`, 'success')
  } catch (err) {
    notify(`保存解析器设置失败：${err.message}`, 'error')
  } finally {
    pluginPrefLoading.value = false
  }
}

async function loginPluginCredentials() {
  const item = editorSource.value
  const form = item ? forms[item.id] : null
  if (!item || !form) return
  pluginLoginBusy.value = true
  try {
    const result = await api.pluginCredentialLogin(item.id, { username: form.username, password: form.password })
    if (result?.result?.authorized === false || result?.result?.accepted === false) {
      throw new Error('站点未接受这组账号密码')
    }
    await api.saveAccount(item.id, {
      username: form.username,
      password: form.password,
      login: false,
      settings: {},
    })
    notify(`${item.name} 登录成功`, 'success')
    await loadPluginSettings(item)
  } catch (err) {
    notify(`${item.name}：${err.message}`, 'error')
  } finally {
    pluginLoginBusy.value = false
  }
}

async function saveSource(item, login = false) {
  const form = forms[item.id]
  if (!form) return
  busy.value = `source-${item.id}`
  try {

    const payload = {
      homeUrl: form.homeUrl,
      login,
      settings: {
        chapterConcurrency: Number(form.chapterConcurrency) || 1,
        pageConcurrency: Number(form.pageConcurrency) || 4,
        batchSize: Number(form.batchSize) || 0,
        batchIntervalMinutes: Number(form.batchIntervalMinutes) || 0,
      },
    }
    if (form.username) payload.username = form.username
    if (form.password) payload.password = form.password
    if (form.cookie) payload.cookie = form.cookie
    await api.saveAccount(item.id, payload)
    form.cookie = ''
    notify(login ? `${item.name} 登录成功，设置已保存` : `${item.name} 设置已保存`, 'success')
    editorOpen.value = false
    await load()
  } catch (err) {
    notify(`${item.name}：${err.message}`, 'error')
  } finally {
    busy.value = ''
  }
}

async function toggleHidden(item) {
  busy.value = `source-${item.id}`
  try {
    const result = await api.updateSource(item.id, { hidden: !item.hidden })
    item.hidden = Boolean(result.hidden)
    notify(item.hidden ? `${item.name} 已停用，不再参与探索发现` : `${item.name} 已启用`, 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = ''
  }
}

async function disconnect(item) {
  const confirmed = await askConfirm({
    title: '清除登录凭据',
    message: `确定清除「${item.name}」的登录凭据吗？`,
    confirmText: '清除',
    danger: true,
  })
  if (!confirmed) return
  busy.value = `source-${item.id}`
  try {
    await api.deleteAccount(item.id)
    const form = forms[item.id]
    form.username = ''
    form.password = ''
    form.cookie = ''
    notify(`${item.name} 的凭据已清除`, 'success')
    await load()
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = ''
  }
}

function openRepository() {
  Object.assign(repositoryForm, { name: '', url: '', kind: 'json' })
  editingRepositoryId.value = ''
  repositoryOpen.value = true
}

function openRepositoryEdit(item) {
  editingRepositoryId.value = item.id
  Object.assign(repositoryForm, { name: item.name || '', url: item.url || '', kind: item.kind || 'json' })
  repositoryOpen.value = true
}

async function createRepository() {
  if (!repositoryForm.name.trim() || !repositoryForm.url.trim()) {
    notify('仓库名称和地址不能为空', 'warning')
    return
  }
  busy.value = 'repository-create'
  try {
    const payload = {
      name: repositoryForm.name.trim(),
      url: repositoryForm.url.trim(),
      kind: repositoryForm.kind,
    }
    let saved
    if (editingRepositoryId.value) {
      const updated = await api.updateRepository(editingRepositoryId.value, payload)
      saved = updated && updated.id ? updated : { ...payload, id: editingRepositoryId.value }
      const index = repositories.value.findIndex((row) => row.id === editingRepositoryId.value)
      if (index >= 0) repositories.value[index] = saved
      notify(`${saved.name || payload.name} 已更新`, 'success')
    } else {
      saved = await api.createRepository(payload)
      repositories.value = [...repositories.value.filter((item) => item.id !== saved.id), saved]
      notify(`${saved.name} 已添加，正在同步来源清单`, 'success')
    }
    repositoryOpen.value = false
    addRepositoryID.value = saved.id
    expandedRepositories.value[saved.id] = true
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = ''
  }
}

async function syncRepository(item) {
  busy.value = `repository-${item.id}`
  try {
    const result = await api.syncRepository(item.id)
    const index = repositories.value.findIndex((row) => row.id === item.id)
    if (index >= 0) repositories.value[index] = result.repository || item
    expandedRepositories.value[item.id] = true
    notify(`${item.name} 已同步 ${result.items?.length || 0} 个来源`, 'success')
  } catch (err) {
    notify(`${item.name} 同步失败：${err.message}`, 'error')
  } finally {
    busy.value = ''
  }
}

async function removeRepository(item) {
  const confirmed = await askConfirm({
    title: '删除拓展仓库',
    message: `确定删除拓展仓库「${item.name}」吗？`,
    confirmText: '删除',
    danger: true,
  })
  if (!confirmed) return
  busy.value = `repository-${item.id}`
  try {
    await api.deleteRepository(item.id)
    repositories.value = repositories.value.filter((row) => row.id !== item.id)
    if (addRepositoryID.value === item.id) addRepositoryID.value = repositories.value[0]?.id || ''
    notify(`${item.name} 已删除`, 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = ''
  }
}

function isKototoroPlugin(repository, extension) {
  const identity = `${repository?.url || ''} ${extension?.packageName || ''} ${extension?.name || ''} ${extension?.pluginType || ''}`.toLowerCase()
  const installUrl = String(extension?.installUrl || '').split('?')[0].toLowerCase()
  const kind = String(repository?.kind || '').toLowerCase()
  const jarLike = installUrl.endsWith('.jar') || installUrl.endsWith('.dex') || kind === 'jar'
  return extension?.runtime === 'plugin' && (extension?.pluginType === 'jar' || jarLike)
}

function canInstallExtension(repository, extension) {
  return Boolean(extension?.installable || isKototoroPlugin(repository, extension))
}

function openAddSource() {
  if (!repositories.value.length) {
    notify('请先在拓展仓库栏目添加仓库', 'warning')
    activeKind.value = 'repository'
    return
  }
  sourceSearch.value = ''
  extensionCandidates.value = []
  candidateFilter.value = 'all'
  addSourceOpen.value = true
  loadRepositoryCandidates(selectedRepository.value)
}

async function loadRepositoryCandidates(repository) {
  if (!repository) return
  const cachedKey = `${repository.id}:${activeKind.value}`
  if (candidateLoadedRepos.value.has(cachedKey)) return
  const extensions = (repository.extensions || []).filter((extension) => canInstallExtension(repository, extension))
  if (!extensions.length) {
    extensionCandidates.value = []
    candidateLoadedRepos.value = new Set([...candidateLoadedRepos.value, cachedKey])
    return
  }
  candidateLoading.value = true
  const collected = []
  const errors = []
  for (const extension of extensions) {
    try {
      const result = await api.importRepositoryExtension(repository.id, extension.id)
      const candidates = Array.isArray(result.candidates) ? result.candidates : (Array.isArray(result.sources) ? result.sources : [])
      collected.push(...candidates)
    } catch (err) {
      errors.push(`${extension.name || extension.id}：${err.message}`)
    }
  }
  const seen = new Set()
  extensionCandidates.value = collected.filter((candidate) => {
    if (!candidate?.id || seen.has(candidate.id)) return false
    seen.add(candidate.id)
    return true
  })
  candidateLoadedRepos.value = new Set([...candidateLoadedRepos.value, cachedKey])
  candidateLoading.value = false
  if (extensionCandidates.value.length) {
    notify(`已加载 ${extensionCandidates.value.length} 个可添加内容源`, 'success')
  } else if (errors.length) {
    notify(`内容源加载失败：${errors[0]}`, 'error')
  }
}

async function loadExtensionCandidates(repository, extension) {
  if (!canInstallExtension(repository, extension)) {
    notify('该来源是 Android/JVM 插件，当前仅登记清单，不能直接执行', 'warning')
    return
  }
  busy.value = `extension-${extension.id}`
  candidateLoading.value = true
  try {
    const result = await api.importRepositoryExtension(repository.id, extension.id)
    const candidates = Array.isArray(result.candidates) ? result.candidates : (Array.isArray(result.sources) ? result.sources : [])
    extensionCandidates.value = candidates
    notify(candidates.length ? `已加载 ${candidates.length} 个可添加内容源` : '该仓库没有可添加内容源', candidates.length ? 'success' : 'info')
  } catch (err) {
    notify(`加载内容源失败：${err.message}`, 'error')
  } finally {
    candidateLoading.value = false
    busy.value = ''
  }
}

async function addCandidate(candidate) {
  if (!candidate || sources.value.some((item) => item.id === candidate.id)) {
    notify('该内容源已经添加', 'info')
    return
  }
  busy.value = `candidate-${candidate.id}`
  try {
    const saved = await api.createCustomSource({
      id: candidate.id,
      name: candidate.name || candidate.sourceName,
      kind: candidate.kind || 'comic',
      description: candidate.description || '',
      homepage: candidate.homepage || '',
      icon: candidate.icon || '',
      repoUrl: candidate.repoUrl || '',
      config: candidate.config || {},
      enabled: true,
    })
    sources.value = [...sources.value, saved]
    extensionCandidates.value = extensionCandidates.value.filter((item) => item.id !== candidate.id)
    notify(`${saved.name} 已添加`, 'success')
  } catch (err) {
    notify(`添加失败：${err.message}`, 'error')
  } finally {
    busy.value = ''
  }
}

function onDragStart(item, event) {
	if (event?.target?.closest?.('button, input, textarea, select, a')) {
		event.preventDefault()
		return
	}
  dragId.value = item.id
  if (event?.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    try {
      event.dataTransfer.setData('text/plain', item.id)
    } catch {
      // 忽略浏览器对 setData 的限制
    }
  }
}

function onDragOver(item, event) {
  if (!dragId.value || dragId.value === item.id) return
  event.preventDefault()
  const list = sources.value
  const from = list.findIndex((row) => row.id === dragId.value)
  const to = list.findIndex((row) => row.id === item.id)
  if (from < 0 || to < 0 || from === to) return
  const next = [...list]
  const [moved] = next.splice(from, 1)
  next.splice(to, 0, moved)
  sources.value = next
}

async function onDrop() {
  dragId.value = ''
  const ids = visibleSources.value.map((item) => item.id)
  if (!ids.length) return
  try {
    await api.updateSource(ids[0], { order: ids })
    notify('来源顺序已保存', 'success')
  } catch (err) {
    notify(`保存排序失败：${err.message}`, 'error')
  }
}

function onDragEnd() {
  dragId.value = ''
}
</script>

<template>
  <div class="toolbar repository-toolbar">
    <div class="segmented" role="tablist" aria-label="资源类型">
      <button type="button" :class="{ active: activeKind === 'comic' }" @click="activeKind = 'comic'">
        漫画源
        <span class="segmented-count">{{ comicSources.length }}</span>
      </button>
      <button type="button" :class="{ active: activeKind === 'book' }" @click="activeKind = 'book'">
        书籍源
        <span class="segmented-count">{{ bookSources.length }}</span>
      </button>
      <button type="button" :class="{ active: activeKind === 'other' }" @click="activeKind = 'other'">
        其他源
        <span class="segmented-count">{{ otherSources.length }}</span>
      </button>
      <button type="button" :class="{ active: activeKind === 'repository' }" @click="activeKind = 'repository'">
        拓展仓库
        <span class="segmented-count">{{ repositories.length }}</span>
      </button>
    </div>
    <span class="spacer" />
    <template v-if="activeKind === 'repository'">
      <button class="btn small" type="button" @click="openRepository">
        <Plus :size="15" />
        添加仓库
      </button>
    </template>
    <template v-else>
      <button class="btn secondary small" type="button" :disabled="loading" @click="load">
        <RefreshCw :size="15" :class="{ spin: loading }" />
        刷新
      </button>
      <button class="btn small" type="button" @click="openAddSource">
        <Plus :size="15" />
        添加源
      </button>
    </template>
  </div>

  <div v-if="loading && !sources.length" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载资源库</span>
  </div>

  <template v-else-if="activeKind !== 'repository'">
    <div v-if="!visibleSources.length" class="card empty">
      <Server :size="24" />
      <p>暂无{{ kindLabel(activeKind) }}来源</p>
    </div>
    <div v-else class="source-grid source-grid-simple">
      <article
        v-for="(item, index) in visibleSources"
        :key="item.id"
        class="card source-card-simple"
        :class="{ 'source-card-disabled': item.hidden, 'source-card-dragging': dragId === item.id, 'source-card-selected': isSourceSelected(item) }"
        draggable="true"
        @dragstart="onDragStart(item, $event)"
        @contextmenu.prevent="enterSourceSelection(item, index, $event)"
        @dragover="onDragOver(item, $event)"
        @drop.prevent="onDrop"
        @dragend="onDragEnd"
      >
        <button
          class="source-logo-button"
          type="button"
          :disabled="busy === `source-${item.id}`"
          @click="toggleHidden(item)"
        >
          <Loader2 v-if="busy === `source-${item.id}`" :size="20" class="spin" />
          <img v-else-if="item.icon" :src="item.icon" :alt="item.name" />
          <Server v-else :size="22" />
        </button>
        <div class="source-simple-copy">
          <h2>{{ item.name }}</h2>
          <span class="badge" :class="sourceStatus(item).className">{{ sourceStatus(item).label }}</span>
        </div>
        <div class="source-card-actions">
        <button
          v-if="isPluginSource(item)"
          class="icon-btn source-verify-button"
          type="button"
          aria-label="浏览器验证"
          :disabled="busy === `browser-${item.id}`"
          @click="openBrowserCheck(item)"
        >
          <Loader2 v-if="busy === `browser-${item.id}`" :size="17" class="spin" />
          <ShieldCheck v-else :size="17" />
        </button>
        <button
          class="icon-btn source-category-button"
          type="button"
          aria-label="筛选分类"
          @click="openCategory(item)"
        >
          <SlidersHorizontal :size="17" />
        </button>
        <button
          class="icon-btn source-more-button"
          type="button"
          aria-label="编辑来源"
          @click="openEditor(item)"
        >
          <MoreVertical :size="19" />
        </button>
        </div>
      </article>
    </div>
    <div v-if="sourceSelection.size" class="source-selection-bar">
      <span>已选 {{ selectedSources.length }} 项</span>
      <button class="btn secondary small" type="button" @click="allVisibleSourcesSelected ? clearSourceSelection() : selectAllSources()">
        {{ allVisibleSourcesSelected ? '取消全选' : '全选' }}
      </button>
      <button class="btn secondary small" type="button" @click="clearSourceSelection">清除</button>
      <button class="btn danger small" type="button" :disabled="busy === 'source-batch-delete'" @click="removeSelectedSources">
        <Trash2 :size="14" />删除
      </button>
    </div>
  </template>

  <template v-else>
    <div v-if="!repositories.length" class="card empty repository-empty">
      <Boxes :size="28" />
      <p>还没有拓展仓库。添加 Kototoro、Mihon 或其他兼容仓库后即可同步来源。</p>
      <button class="btn small" type="button" @click="openRepository">
        <Plus :size="15" />
        添加第一个仓库
      </button>
    </div>
    <div v-else class="repository-list">
      <article v-for="repository in repositories" :key="repository.id" class="card repository-card">
        <header class="repository-head">
          <button class="repository-toggle" type="button" @click="expandedRepositories[repository.id] = !expandedRepositories[repository.id]">
            <ChevronDown :size="18" :class="{ rotated: expandedRepositories[repository.id] }" />
            <span class="repository-icon"><Boxes :size="20" /></span>
            <span class="repository-title">
              <strong>{{ repository.name }}</strong>
              <small>{{ repository.url }}</small>
            </span>
          </button>
          <div class="repository-actions">
            <span class="badge">{{ repository.kind || 'json' }}</span>
            <span class="badge" :class="repository.status === 'ok' ? 'success' : repository.status === 'error' ? 'danger' : ''">
              {{ repository.status === 'ok' ? `${repository.extensions?.length || 0} 个来源` : repository.status === 'error' ? '同步失败' : '待同步' }}
            </span>
            <button class="btn secondary small" type="button" :disabled="busy === `repository-${repository.id}`" @click="syncRepository(repository)">
              <Loader2 v-if="busy === `repository-${repository.id}`" :size="14" class="spin" />
              <RefreshCw v-else :size="14" />
              同步
            </button>
            <button class="btn secondary small" type="button" @click="openRepositoryEdit(repository)">
              <Pencil :size="14" />
              编辑
            </button>
            <button class="btn danger small" type="button" :disabled="busy === `repository-${repository.id}`" @click="removeRepository(repository)">
              <Trash2 :size="14" />
            </button>
          </div>
        </header>
        <p v-if="repository.error" class="repository-error">{{ repository.error }}</p>
        <div v-if="expandedRepositories[repository.id]" class="extension-list">
          <div v-if="!repository.extensions?.length" class="empty compact">同步后在这里显示仓库来源。</div>
          <article v-for="extension in repository.extensions || []" :key="extension.id" class="extension-row">
            <img v-if="extension.icon" :src="extension.icon" alt="" />
            <PackagePlus v-else :size="19" />
            <div class="extension-meta">
              <strong>{{ extension.name }}</strong>
              <small>
                {{ extension.packageName || extension.kind }}<template v-if="extension.version"> · v{{ extension.version }}</template>
              </small>
            </div>
            <span class="badge">{{ extension.kind }}</span>
            <button
              class="btn small"
              type="button"
              :class="{ secondary: !canInstallExtension(repository, extension) }"
              :disabled="busy === `extension-${extension.id}`"
              @click="loadExtensionCandidates(repository, extension)"
            >
              <Loader2 v-if="busy === `extension-${extension.id}`" :size="14" class="spin" />
              <Check v-else-if="canInstallExtension(repository, extension)" :size="14" />
              <ShieldCheck v-else :size="14" />
              {{ canInstallExtension(repository, extension) ? '加载内容源' : '暂不支持' }}
            </button>
          </article>
        </div>
      </article>
    </div>
  </template>

  <div v-if="addSourceOpen" class="modal-backdrop" @click.self="addSourceOpen = false">
    <div class="modal modal-lg source-add-modal">
      <div class="modal-head">
        <div>
          <h2>添加内容源</h2>
          <p class="muted small">从拓展仓库中选择要加入资源库的内容源，添加后可在探索发现中使用。</p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="addSourceOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <div class="source-add-toolbar">
          <RoundedSelect v-model="addRepositoryID" label="" :options="repositoryOptions" placeholder="选择仓库" />
          <RoundedSelect
            v-if="extensionCandidates.length"
            v-model="candidateFilter"
            label=""
            :options="candidateFilterOptions"
            placeholder="全部分类"
          />
          <label class="source-repository-search">
            <Search :size="15" />
            <input v-model="sourceSearch" placeholder="搜索内容源" />
          </label>
        </div>
        <div v-if="candidateLoading" class="empty compact">
          <Loader2 :size="20" class="spin" />
          <span>正在加载仓库内容源</span>
        </div>
        <div v-else-if="visibleCandidates.length" class="source-add-list">
          <article v-for="candidate in visibleCandidates" :key="candidate.id" class="source-add-row">
            <img v-if="candidate.icon" :src="candidate.icon" alt="" />
            <PackagePlus v-else :size="20" />
            <div class="extension-meta">
              <strong>{{ candidate.name }}</strong>
              <small>{{ candidateCategoryLabel(candidate.contentType || candidate.kind) }} · {{ candidate.sourceName }}</small>
            </div>
            <span class="badge">{{ candidate.kind === 'book' ? '书籍' : candidate.kind === 'other' ? '其他' : '漫画' }}</span>
            <button
              class="btn small"
              type="button"
              :disabled="busy === `candidate-${candidate.id}`"
              @click="addCandidate(candidate)"
            >
              <Loader2 v-if="busy === `candidate-${candidate.id}`" :size="14" class="spin" />
              <Plus v-else :size="14" />
              添加
            </button>
          </article>
        </div>
        <div v-else-if="selectedExtensions.length" class="source-add-list">
          <article v-for="extension in selectedExtensions" :key="extension.id" class="source-add-row">
            <img v-if="extension.icon" :src="extension.icon" alt="" />
            <PackagePlus v-else :size="20" />
            <div class="extension-meta">
              <strong>{{ extension.name }}</strong>
              <small>{{ extension.packageName || extension.kind }}<template v-if="extension.version"> · v{{ extension.version }}</template></small>
            </div>
            <span class="badge">{{ extension.kind }}</span>
            <button
              class="btn small"
              type="button"
              :class="{ secondary: !canInstallExtension(selectedRepository, extension) }"
              :disabled="busy === `extension-${extension.id}` || !canInstallExtension(selectedRepository, extension)"
              @click="loadExtensionCandidates(selectedRepository, extension)"
            >
              <Loader2 v-if="busy === `extension-${extension.id}`" :size="14" class="spin" />
              <Check v-else-if="canInstallExtension(selectedRepository, extension)" :size="14" />
              <ShieldCheck v-else :size="14" />
              {{ canInstallExtension(selectedRepository, extension) ? '加载内容源' : '暂不支持' }}
            </button>
          </article>
        </div>
        <div v-else class="empty compact">该仓库没有可添加的内容源，或仓库尚未同步。</div>
      </div>
    </div>
  </div>

  <div v-if="categoryOpen" class="modal-backdrop" @click.self="categoryOpen = false">
    <div class="modal modal-lg">
      <div class="modal-head">
        <div>
          <h2>分类显示设置</h2>
          <p class="muted small">{{ categorySource?.name }} · 每个分类独立管理，取消勾选后不参与探索发现</p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="categoryOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <section v-for="group in categoryFilters(categorySource || {})" :key="group.key" class="settings-block category-group">
          <div class="category-group-head">
            <h3>{{ group.label }}</h3>
            <label class="category-toggle category-all-toggle">
              <input type="checkbox" :checked="groupAllSelected(group)" @change="toggleGroupAll(group)" />
              <span>全部</span>
            </label>
          </div>
          <div class="category-picker-grid">
            <label v-for="option in group.options || []" :key="`${group.key}-${option.value}`" class="category-toggle">
              <input
                type="checkbox"
                :checked="(categorySelection[group.key] || []).includes(String(option.value))"
                @change="toggleCategory(group, option.value)"
              />
              <span>{{ option.label }}</span>
            </label>
          </div>
        </section>
      </div>
      <div class="modal-foot">
        <button class="btn secondary" type="button" @click="categoryOpen = false">取消</button>
        <button class="btn" type="button" :disabled="busy === `category-${categorySource?.id}`" @click="saveCategories">
          <Loader2 v-if="busy === `category-${categorySource?.id}`" :size="15" class="spin" />
          <Save v-else :size="15" />
          保存分类设置
        </button>
      </div>
    </div>
  </div>


  <div v-if="browserOpen && browserSource" class="modal-backdrop" @click.self="browserOpen = false">
    <section class="modal browser-verify-modal">
      <header class="modal-head">
        <h2>浏览器验证 · {{ browserSource.name }}</h2>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="browserOpen = false">
          <X :size="17" />
        </button>
      </header>
      <div class="modal-body">
        <p class="muted small">先在浏览器打开验证地址并完成站点验证，再把该站点 Cookie 粘贴到下方导入。导入后解析器请求会复用这组会话。</p>
        <label class="field">
          <span>验证地址</span>
          <input class="input" :value="browserSession.url" readonly />
        </label>
        <label class="field">
          <span>导入 Cookie</span>
          <textarea
            v-model="browserCookies"
            class="textarea cookie-textarea"
            placeholder="每行一条，例如：cf_clearance=...; Path=/; Domain=.example.com"
          ></textarea>
        </label>
      </div>
      <footer class="modal-foot">
        <button class="btn secondary" type="button" @click="browserOpen = false">取消</button>
        <button
          class="btn"
          type="button"
          :disabled="busy === `browser-${browserSource.id}`"
          @click="importBrowserCookies"
        >
          <Loader2 v-if="busy === `browser-${browserSource.id}`" :size="15" class="spin" />
          <ShieldCheck v-else :size="15" />
          导入 Cookie
        </button>
      </footer>
    </section>
  </div>
  <div v-if="editorOpen && editorSource && forms[editorSource.id]" class="modal-backdrop" @click.self="editorOpen = false">
    <div class="modal modal-lg">
      <div class="modal-head">
        <div class="inline">
          <img v-if="editorSource.icon" class="source-editor-icon" :src="editorSource.icon" alt="" />
          <div>
            <h2>{{ editorSource.name }}</h2>
            <p class="muted small">{{ sourceStatus(editorSource).label }}</p>
          </div>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="editorOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <section class="source-editor-section">
          <h3>{{ isPluginSource(editorSource) ? '解析器账号与设置' : '网站与凭据' }}</h3>
          <template v-if="isPluginSource(editorSource)">
            <div v-if="pluginAuthLoading || pluginPrefLoading" class="inline muted small">
              <Loader2 :size="15" class="spin" />
              <span>正在读取解析器设置</span>
            </div>
            <template v-else>
              <div class="plugin-auth-summary">
                <span class="badge" :class="pluginAuth?.capabilities?.authorized ? 'success' : ''">
                  {{ pluginAuth?.capabilities?.authorized ? '已授权' : '未授权' }}
                </span>
                <span class="muted small">
                  {{ pluginAuth?.capabilities?.credentialsSupported ? '支持账号密码登录' : '该来源不需要账号密码' }}
                  <template v-if="pluginAuth?.capabilities?.browserSupported"> · 支持浏览器验证</template>
                </span>
              </div>
              <div v-if="pluginAuth?.capabilities?.credentialsSupported" class="settings-grid">
                <label class="field">
                  <span>账号</span>
                  <input v-model="forms[editorSource.id].username" class="input" autocomplete="username" />
                </label>
                <label class="field">
                  <span>密码</span>
                  <PasswordInput v-model="forms[editorSource.id].password" autocomplete="current-password" />
                </label>
              </div>
              <button
                v-if="pluginAuth?.capabilities?.credentialsSupported"
                class="btn small"
                type="button"
                :disabled="pluginLoginBusy"
                @click="loginPluginCredentials"
              >
                <Loader2 v-if="pluginLoginBusy" :size="14" class="spin" />
                <KeyRound v-else :size="14" />
                登录并保存
              </button>
              <button
                v-if="pluginAuth?.capabilities?.browserSupported"
                class="btn secondary small"
                type="button"
                :disabled="busy === `browser-${editorSource.id}`"
                @click="openBrowserCheck(editorSource)"
              >
                <ShieldCheck :size="14" />
                浏览器验证
              </button>
              <div v-for="node in pluginPreferenceNodes()" :key="node.id" class="plugin-preference-row">
                <label class="field">
                  <span>{{ node.title }}</span>
                  <select
                    v-if="node.kind === 'CHOICE'"
                    class="input"
                    :value="pluginPreferenceValue(node)"
                    @change="setPluginPreferenceValue(node, $event.target.value); savePluginPreference(node)"
                  >
                    <option v-for="choice in node.choices || []" :key="choice.value" :value="choice.value">{{ choice.title }}</option>
                  </select>
                  <label v-else-if="node.kind === 'TOGGLE'" class="switch-row">
                    <input
                      type="checkbox"
                      :checked="Boolean(pluginPreferenceValue(node))"
                      @change="setPluginPreferenceValue(node, $event.target.checked); savePluginPreference(node)"
                    />
                    <span>{{ node.summary || '启用' }}</span>
                  </label>
                  <input
                    v-else
                    class="input"
                    :value="pluginPreferenceValue(node)"
                    @input="setPluginPreferenceValue(node, $event.target.value)"
                    @change="savePluginPreference(node)"
                  />
                </label>
              </div>
            </template>
          </template>
          <template v-else>
            <label class="field">
              <span>网站地址（留空自动优选）</span>
              <SitePicker
                v-model="forms[editorSource.id].homeUrl"
                :options="editorSource.sites || []"
                placeholder="留空自动选择，优选可用地址"
              />
            </label>
            <template v-if="editorSource.id === 'picacg'">
              <div class="settings-grid">
                <label class="field">
                  <span>账号（邮箱或用户名）</span>
                  <input v-model="forms[editorSource.id].username" class="input" autocomplete="username" />
                </label>
                <label class="field">
                  <span>密码</span>
                  <PasswordInput v-model="forms[editorSource.id].password" autocomplete="current-password" />
                </label>
              </div>
            </template>
            <label v-else class="field">
              <span>Cookie（可选，可拖动调整高度）</span>
              <textarea
                v-model="forms[editorSource.id].cookie"
                class="textarea cookie-textarea"
                placeholder="粘贴浏览器中的 Cookie"
              />
            </label>
          </template>
        </section>

        <section class="source-editor-section">
          <h3>下载策略</h3>
          <div class="settings-grid settings-grid-4">
            <label class="field">
              <span>章节线程</span>
              <input v-model.number="forms[editorSource.id].chapterConcurrency" class="input" type="number" min="1" max="8" />
            </label>
            <label class="field">
              <span>图片线程</span>
              <input v-model.number="forms[editorSource.id].pageConcurrency" class="input" type="number" min="1" max="16" />
            </label>
            <label class="field">
              <span>每下载 N 话暂停</span>
              <input v-model.number="forms[editorSource.id].batchSize" class="input" type="number" min="0" max="1000" />
            </label>
            <label class="field">
              <span>暂停分钟</span>
              <input v-model.number="forms[editorSource.id].batchIntervalMinutes" class="input" type="number" min="0" max="10080" />
            </label>
          </div>
          <p class="muted small">留空或填 0 表示不启用批量暂停。</p>
        </section>
      </div>
      <div class="modal-foot source-editor-foot">
        <button
          v-if="accountOf(editorSource.id)"
          class="btn danger small"
          type="button"
          :disabled="busy === `source-${editorSource.id}`"
          @click="disconnect(editorSource)"
        >
          <Trash2 :size="14" />
          清除凭据
        </button>
        <span class="spacer" />
        <button class="btn secondary" type="button" @click="editorOpen = false">取消</button>
        <button
          class="btn"
          type="button"
          :disabled="busy === `source-${editorSource.id}`"
          @click="saveSource(editorSource, false)"
        >
          <Loader2 v-if="busy === `source-${editorSource.id}`" :size="15" class="spin" />
          <Save v-else :size="15" />
          保存设置
        </button>
        <button
          v-if="editorSource.id === 'picacg'"
          class="btn"
          type="button"
          :disabled="busy === `source-${editorSource.id}`"
          @click="saveSource(editorSource, true)"
        >
          <KeyRound :size="15" />
          登录并保存
        </button>
      </div>
    </div>
  </div>

  <div v-if="repositoryOpen" class="modal-backdrop" @click.self="repositoryOpen = false">
    <div class="modal">
      <div class="modal-head">
        <div>
          <h2>{{ editingRepositoryId ? '编辑拓展仓库' : '添加拓展仓库' }}</h2>
          <p class="muted small">{{ editingRepositoryId ? '修改仓库名称、地址或类型，保存后会重新同步来源。' : '添加兼容 Kototoro、Mihon、JAR 等格式的仓库清单地址。' }}</p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="repositoryOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <label class="field">
          <span>仓库名称</span>
          <input v-model="repositoryForm.name" class="input" placeholder="Kototoro Parsers" />
        </label>
        <label class="field">
          <span>仓库地址</span>
          <input v-model="repositoryForm.url" class="input" placeholder="https://example.com/index.min.json" />
        </label>
        <label class="field">
          <span>仓库类型</span>
          <select v-model="repositoryForm.kind" class="input">
            <option v-for="kind in repositoryKinds" :key="kind.value" :value="kind.value">{{ kind.label }}</option>
          </select>
        </label>
      </div>
      <div class="modal-foot">
        <button class="btn secondary" type="button" @click="repositoryOpen = false">取消</button>
        <button class="btn" type="button" :disabled="busy === 'repository-create'" @click="createRepository">
          <Loader2 v-if="busy === 'repository-create'" :size="15" class="spin" />
          <Plus v-else :size="15" />
          {{ editingRepositoryId ? '保存并同步' : '添加并同步' }}
        </button>
      </div>
    </div>
  </div>
</template>
