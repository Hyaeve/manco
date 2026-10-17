<script setup>
import { computed, nextTick, onDeactivated, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import {
  Boxes,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Download,
  Eye,
  EyeOff,
  KeyRound,
  Loader2,
  MoreVertical,
  Search,
  PackagePlus,
  Images,
  RotateCcw,
  Plus,
  RefreshCw,
  Pencil,
  Save,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  Trash2,
  Upload,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import { askConfirm } from '../stores/confirm'
import { notify } from '../stores/notices'
import PasswordInput from '../components/PasswordInput.vue'
import DirectoryPicker from '../components/DirectoryPicker.vue'
import SitePicker from '../components/SitePicker.vue'
import SourceIcon from '../components/SourceIcon.vue'

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
const addUseProxy = ref(true)
const sourceSearch = ref('')
const categoryOpen = ref(false)
const categoryLoading = ref('')
const categorySource = ref(null)
const categorySelection = ref({})
const editorOpen = ref(false)
const editorSource = ref(null)
const expandedRepositories = ref({})
const forms = reactive({})
const repositoryForm = reactive({ name: '', url: '', kind: 'json' })
const editingRepositoryId = ref('')
const dragId = ref('')
const dragOverId = ref('')
const browserOpen = ref(false)
const browserSource = ref(null)
const browserSession = reactive({ url: '', userAgent: '' })
const browserCookies = ref('')
const sourceSelection = ref(new Set())
const sourceAnchor = ref(-1)
const extensionCandidates = ref([])
// 枚举内容源要先把插件装好并重启 JVM sidecar，单个扩展就要数秒；结果按
// 「仓库ID:来源类型」缓存到 localStorage，重开窗口直接命中，不再重复安装。
const CANDIDATE_CACHE_KEY = 'manco-add-source-candidates-v1'
const CANDIDATE_TIMEOUT_MS = 90000
const CANDIDATE_CONCURRENCY = 2
const candidateCache = ref(readCandidateCacheFromStorage())
const candidateFilter = ref('all')
const repositoryStripRef = ref(null)
const typeStripRef = ref(null)
const stripBounds = reactive({
  repository: { left: false, right: false },
  type: { left: false, right: false },
})
const iconLibraryOpen = ref(false)
const iconLibrary = ref([])
const iconLibraryQuery = ref('')
const iconUrlInput = ref('')
const iconBusy = ref('')
const iconUploading = ref(false)
const iconDownloading = ref(false)
const iconUploadInput = ref(null)
const candidateLoading = ref(false)
// 命中本地缓存的仓库视为已加载，避免打开窗口时又整批重装插件。
const candidateLoadedRepos = ref(new Set(Object.keys(candidateCache.value)))
const candidateProgress = reactive({ done: 0, total: 0, label: '' })
const candidateCancel = ref(false)
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
const repositoryFilterOptions = computed(() => [
  { value: 'all', label: '全部仓库' },
  ...repositories.value.map((repository) => ({ value: repository.id, label: repository.name })),
])
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
    HENTAI_VIDEO: '成人视频',
    OTHER: '其他',
  }[value] || value || '其他'
}

const candidateFilterOptions = computed(() => {
  if (activeKind.value === 'book') {
    return ['all', 'NOVEL', 'HENTAI_NOVEL'].map((value) => ({ value, label: value === 'all' ? '全部类型' : candidateCategoryLabel(value) }))
  }
  if (activeKind.value === 'other') {
    return ['all', 'VIDEO', 'HENTAI_VIDEO', 'OTHER'].map((value) => ({ value, label: value === 'all' ? '全部类型' : candidateCategoryLabel(value) }))
  }
  return ['all', 'MANGA', 'MANHWA', 'HENTAI_MANGA', 'COMICS'].map((value) => ({ value, label: value === 'all' ? '全部类型' : candidateCategoryLabel(value) }))
})

const candidateCategoryIcon = (value) => value || 'OTHER'

function candidateType(item) {
  return String(item.contentType || item.kind || '').trim().toUpperCase()
}

function matchesCandidateType(item, filter) {
  if (filter === 'all') return true
  const type = candidateType(item)
  if (filter === 'MANGA') return ['MANGA', 'MANHUA', 'COMIC'].includes(type)
  return type === filter
}

const visibleCandidates = computed(() => {
  const keyword = sourceSearch.value.trim().toLowerCase()
  return extensionCandidates.value.filter((item) => {
    if (activeKind.value !== 'repository' && (item.kind || 'comic') !== activeKind.value) return false
    if (!matchesCandidateType(item, candidateFilter.value)) return false
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

onMounted(() => {
  window.addEventListener('keydown', onSourceKeydown)
  window.addEventListener('resize', updateAllStripBounds)
  nextTick(updateAllStripBounds)
})
onUnmounted(() => {
  window.removeEventListener('keydown', onSourceKeydown)
  window.removeEventListener('resize', updateAllStripBounds)
})

// 切走页面时退出多选。
onDeactivated(() => {
  clearSourceSelection()
})

function onSourceKeydown(event) {
  if (event.key === 'Escape' && sourceSelection.value.size) clearSourceSelection()
}

watch(addRepositoryID, () => {
  candidateFilter.value = 'all'
  syncExtensionCandidates()
})

// 切换仓库/类型只重排展示，不再自动批量安装插件；要枚举内容源由用户显式触发。
watch(selectedRepository, () => {
  syncExtensionCandidates()
})

watch(activeKind, () => {
  candidateFilter.value = 'all'
  syncExtensionCandidates()
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
    if (!addRepositoryID.value || (addRepositoryID.value !== 'all' && !repositories.value.some((item) => item.id === addRepositoryID.value))) {
      addRepositoryID.value = 'all'
    }
    for (const item of sources.value) {
      const account = accounts.value[item.id] || {}
      const settings = account.settings || {}
      if (!forms[item.id]) {
        forms[item.id] = reactive({
      name: item.name || '',
          username: account.username || '',
          password: account.password || '',
          cookie: '',
          userAgent: account.userAgent || '',
          homeUrl: account.homeUrl || '',
          reloginDays: Number(account.reloginDays ?? 7),
          useProxy: account.useProxy !== false,
          chapterConcurrency: Number(settings.chapterConcurrency ?? 1),
          pageConcurrency: Number(settings.pageConcurrency ?? 2),
          batchSize: Number(settings.batchSize ?? 5),
          batchIntervalMinutes: Number(settings.batchIntervalMinutes ?? 5),
          defaultDownloadDir: settings.defaultDownloadDir || '',
        })
      } else {
        forms[item.id].username = account.username || ''
        if (account.password) forms[item.id].password = account.password
        forms[item.id].homeUrl = account.homeUrl || ''
        forms[item.id].userAgent = account.userAgent || ''
        forms[item.id].reloginDays = Number(account.reloginDays ?? 7)
        forms[item.id].useProxy = account.useProxy !== false
        forms[item.id].chapterConcurrency = Number(settings.chapterConcurrency ?? 1)
        forms[item.id].pageConcurrency = Number(settings.pageConcurrency ?? 2)
        forms[item.id].batchSize = Number(settings.batchSize ?? 5)
        forms[item.id].batchIntervalMinutes = Number(settings.batchIntervalMinutes ?? 5)
        forms[item.id].defaultDownloadDir = settings.defaultDownloadDir || ''
      }
    }
  } catch (err) {
    notify(`资源拓展加载失败：${err.message}`, 'error')
  } finally {
    loading.value = false
  }
}

function accountOf(id) {
  return accounts.value[id] || null
}

function isJMComic(item) {
  return item && String(item.id).toLowerCase() === 'jmcomic'
}

// 哔咔需要按周期重新登录，避免 Token 过期导致浏览与下载失败。
function supportsRelogin(item) {
  return item && String(item.id).toLowerCase() === 'picacg'
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

// 右键进入选择态后，左键单击卡片可以继续多选或取消选中（点击功能
// 按钮时不参与选择，避免误触）。
function handleSourceCardClick(item, index, event) {
  if (!sourceSelection.value.size) return
  if (event?.target?.closest?.('button')) return
  if (item.builtin) return
  event?.preventDefault?.()
  if (event?.shiftKey && sourceAnchor.value >= 0) {
    enterSourceSelection(item, index, event)
    return
  }
  const next = new Set(sourceSelection.value)
  if (next.has(item.id)) next.delete(item.id)
  else next.add(item.id)
  if (!next.size) {
    clearSourceSelection()
    return
  }
  sourceSelection.value = next
  sourceAnchor.value = index
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

async function openCategory(item) {
  let groups = categoryFilters(item)
  if (!groups.length && !item.filtersLoaded) {
    categoryLoading.value = item.id
    try {
      const result = await api.sourceFilters(item.id)
      item.filters = Array.isArray(result.items) ? result.items : []
      item.filtersLoaded = true
      groups = categoryFilters(item)
    } catch (err) {
      notify(`${item.name} 分类加载失败：${err.message}`, 'error')
      return
    } finally {
      categoryLoading.value = ''
    }
  }
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
    item.filtersLoaded = true
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

// 需要登录的内置来源（哔咔、Z-Library 等）都显示账号密码输入框。
function showsAccountLogin(item) {
  if (!item || isPluginSource(item)) return false
  return item.needsLogin || item.id === 'picacg' || item.id === 'zlibrary'
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

async function loadIconLibrary() {
  try {
    const payload = await api.sourceIcons()
    iconLibrary.value = payload.items || []
  } catch (err) {
    iconLibrary.value = []
    notify(`本地图标库加载失败：${err.message}`, 'error')
  }
}

function openIconLibrary() {
  iconLibraryOpen.value = true
  iconLibraryQuery.value = ''
  iconUrlInput.value = ''
  loadIconLibrary()
}

// 图标库关键词搜索：按图标名称 / ID 过滤。
const filteredIconLibrary = computed(() => {
  const keyword = iconLibraryQuery.value.trim().toLowerCase()
  if (!keyword) return iconLibrary.value
  return iconLibrary.value.filter((icon) => {
    const name = String(icon.name || icon.id || '').toLowerCase()
    return name.includes(keyword)
  })
})

// 上传本地图片到 /data/icon，并直接应用为当前内容源图标。
async function uploadIconFile(event) {
  const file = event.target.files?.[0]
  event.target.value = ''
  if (!file) return
  const item = editorSource.value
  if (!item) return
  iconUploading.value = true
  try {
    const uploaded = await api.uploadIcon(file)
    await loadIconLibrary()
    if (uploaded?.item?.id) {
      const result = await api.updateSourceIcon(item.id, { action: 'select', iconId: uploaded.item.id })
      item.icon = `${result?.icon || ''}?t=${Date.now()}`
    }
    notify('图标已上传并应用到该内容源', 'success')
  } catch (err) {
    notify(`上传图标失败：${err.message}`, 'error')
  } finally {
    iconUploading.value = false
  }
}

// 输入网址下载图标到 /data/icon，并直接应用为当前内容源图标。
async function downloadIconFromUrl() {
  const raw = iconUrlInput.value.trim()
  if (!raw) {
    notify('请输入图标地址', 'warning')
    return
  }
  const item = editorSource.value
  if (!item) return
  iconDownloading.value = true
  try {
    const downloaded = await api.downloadIcon(raw)
    await loadIconLibrary()
    if (downloaded?.item?.id) {
      const result = await api.updateSourceIcon(item.id, { action: 'select', iconId: downloaded.item.id })
      item.icon = `${result?.icon || ''}?t=${Date.now()}`
    }
    iconUrlInput.value = ''
    notify(`图标已下载并应用到 ${item.name}`, 'success')
  } catch (err) {
    notify(`下载图标失败：${err.message}`, 'error')
  } finally {
    iconDownloading.value = false
  }
}

async function chooseSourceIcon(icon) {
  const item = editorSource.value
  if (!item) return
  iconBusy.value = item.id
  try {
    const result = await api.updateSourceIcon(item.id, { action: 'select', iconId: icon.id })
    item.icon = `${result?.icon || ''}?t=${Date.now()}`
    iconLibraryOpen.value = false
    notify(`${item.name} 图标已更新`, 'success')
  } catch (err) {
    notify(`更新图标失败：${err.message}`, 'error')
  } finally {
    iconBusy.value = ''
  }
}

async function retrySourceIcon(item) {
  iconBusy.value = item.id
  try {
    const result = await api.updateSourceIcon(item.id, { action: 'fetch' })
    item.icon = `${result?.icon || ''}?t=${Date.now()}`
    notify(`${item.name} 图标已重新获取`, 'success')
  } catch (err) {
    // 获取失败时清空图标，界面会回退为内容源类型图标，避免图裂。
    let fellBack = false
    if (!item.icon || item.icon.startsWith('/api/icons/')) {
      item.icon = ''
      fellBack = true
    }
    notify(`${item.name} 图标获取失败${fellBack ? '，已回退为类型图标' : ''}`, 'warning')
  } finally {
    iconBusy.value = ''
  }
}

async function resetSourceIcon(item) {
  iconBusy.value = item.id
  try {
    const result = await api.updateSourceIcon(item.id, { action: 'reset' })
    item.icon = `${result?.icon || ''}?t=${Date.now()}`
    notify(`${item.name} 图标已恢复默认`, 'success')
  } catch (err) {
    notify(`恢复默认图标失败：${err.message}`, 'error')
  } finally {
    iconBusy.value = ''
  }
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
  if (node.kind === 'TOGGLE') value = { operation: 'boolean', value: Boolean(current) }
  else if (node.kind === 'MULTI_CHOICE') value = { operation: 'stringSet', values: Array.isArray(current) ? current : [] }
  else value = { operation: 'string', value: String(current ?? '') }
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
      useProxy: form.useProxy !== false,
      login,
      settings: {
        chapterConcurrency: Number(form.chapterConcurrency) || 1,
        pageConcurrency: Number(form.pageConcurrency) || 4,
        batchSize: Number(form.batchSize) || 0,
        batchIntervalMinutes: Number(form.batchIntervalMinutes) || 0,
        defaultDownloadDir: form.defaultDownloadDir || '',
      },
    }
    if (form.username) payload.username = form.username
    if (form.password) payload.password = form.password
    if (form.cookie) payload.cookie = form.cookie
    if (form.userAgent) payload.userAgent = form.userAgent
    if (supportsRelogin(item)) {
      const days = Number(form.reloginDays)
      payload.reloginDays = Number.isFinite(days) && days > 0 ? Math.round(days) : 7
    }
    await api.saveAccount(item.id, payload)
    // 非内置来源支持自定义显示名称（插件源加进来后名字往往很长）。
    const nextName = String(form.name || '').trim()
    if (!item.builtin && nextName && nextName !== item.name) {
      await api.renameCustomSource(item.id, nextName)
    }
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
  Object.assign(repositoryForm, { name: item.customName || '', url: item.url || '', kind: item.kind || 'json' })
  repositoryOpen.value = true
}

async function createRepository() {
  if (!repositoryForm.url.trim()) {
    notify('仓库地址不能为空', 'warning')
    return
  }
  busy.value = 'repository-create'
  try {
    const payload = {
      url: repositoryForm.url.trim(),
      kind: repositoryForm.kind,
    }
    const trimmedName = repositoryForm.name.trim()
    if (trimmedName) payload.name = trimmedName
    const inferredName = () => {
      try {
        const path = new URL(payload.url).pathname.replace(/\/+$/, '')
        const last = decodeURIComponent(path.split('/').filter(Boolean).pop() || '')
        return last.replace(/\.(min\.)?(json|pb)$/i, '') || '拓展仓库'
      } catch {
        return '拓展仓库'
      }
    }
    let saved
    if (editingRepositoryId.value) {
      const updated = await api.updateRepository(editingRepositoryId.value, payload)
      saved = updated && updated.id ? updated : { ...payload, id: editingRepositoryId.value }
      const index = repositories.value.findIndex((row) => row.id === editingRepositoryId.value)
      if (index >= 0) repositories.value[index] = saved
      notify(`${saved.name || payload.name || inferredName()} 已更新`, 'success')
    } else {
      saved = await api.createRepository(payload)
      repositories.value = [...repositories.value.filter((item) => item.id !== saved.id), saved]
      notify(`${saved.name || inferredName()} 已添加，正在同步来源清单`, 'success')
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

function isMihonFamilyPlugin(repository, extension) {
  const pluginType = String(extension?.pluginType || repository?.kind || '').toLowerCase()
  if (['mihon', 'aniyomi', 'tsundoku', 'kanade'].includes(pluginType)) return true
  const installUrl = String(extension?.installUrl || '').split('?')[0].toLowerCase()
  return installUrl.endsWith('.apk')
}

function canInstallExtension(repository, extension) {
  return Boolean(extension?.installable || isKototoroPlugin(repository, extension) || isMihonFamilyPlugin(repository, extension))
}

function openAddSource() {
  if (!repositories.value.length) {
    notify('请先在拓展仓库栏目添加仓库', 'warning')
    activeKind.value = 'repository'
    return
  }
  sourceSearch.value = ''
  addRepositoryID.value = 'all'
  candidateFilter.value = 'all'
  syncExtensionCandidates()
  addSourceOpen.value = true
}

async function loadRepositoryCandidates(repository, options = {}) {
  const force = Boolean(options.force)
  const targets = repository
    ? [repository]
    : addRepositoryID.value === 'all'
      ? repositories.value
      : [selectedRepository.value].filter(Boolean)
  if (!targets.length) {
    syncExtensionCandidates()
    return
  }

  const kind = activeKind.value
  const tasks = []
  for (const target of targets) {
    const cachedKey = `${target.id}:${kind}`
    if (!force && candidateLoadedRepos.value.has(cachedKey)) continue
    const extensions = (target.extensions || []).filter((extension) => canInstallExtension(target, extension))
    if (!extensions.length) {
      candidateCache.value = { ...candidateCache.value, [cachedKey]: [] }
      candidateLoadedRepos.value = new Set([...candidateLoadedRepos.value, cachedKey])
      continue
    }
    for (const extension of extensions) tasks.push({ target, extension, cachedKey })
  }

  if (!tasks.length) {
    syncExtensionCandidates()
    persistCandidateCache()
    notify('内容源已是最新，无需重复加载', 'info')
    return
  }

  candidateCancel.value = false
  candidateLoading.value = true
  candidateProgress.done = 0
  candidateProgress.total = tasks.length
  candidateProgress.label = ''

  const errors = []
  const collectedByRepo = new Map()
  const failedKeys = new Set()
  const processedKeys = new Set()

  // 每装完一个扩展就刷新一次列表，用户能边加载边挑，也能随时取消。
  const flushCollected = () => {
    if (!collectedByRepo.size) return
    const next = { ...candidateCache.value }
    for (const [cachedKey, rows] of collectedByRepo) {
      const seen = new Set()
      next[cachedKey] = rows.filter((candidate) => {
        if (!candidate?.id || seen.has(candidate.id)) return false
        seen.add(candidate.id)
        return true
      })
    }
    candidateCache.value = next
    syncExtensionCandidates()
  }

  let cursor = 0
  const worker = async () => {
    while (!candidateCancel.value) {
      const index = cursor
      cursor += 1
      if (index >= tasks.length) return
      const { target, extension, cachedKey } = tasks[index]
      processedKeys.add(cachedKey)
      candidateProgress.label = `${target.name} · ${extension.name || extension.id}`
      try {
        const result = await withCandidateTimeout(api.importRepositoryExtension(target.id, extension.id))
        const candidates = Array.isArray(result.candidates) ? result.candidates : (Array.isArray(result.sources) ? result.sources : [])
        const rows = collectedByRepo.get(cachedKey) || []
        rows.push(...candidates.map((candidate) => ({
          ...candidate,
          repositoryId: target.id,
          repositoryName: target.name,
        })))
        collectedByRepo.set(cachedKey, rows)
      } catch (err) {
        failedKeys.add(cachedKey)
        errors.push(`${extension.name || extension.id}：${err.message}`)
      } finally {
        candidateProgress.done += 1
      }
      flushCollected()
    }
  }

  await Promise.all(
    Array.from({ length: Math.min(CANDIDATE_CONCURRENCY, tasks.length) }, () => worker()),
  )

  // 失败的扩展保持未加载，便于下次只补它。
  for (const task of tasks) {
    if (!processedKeys.has(task.cachedKey)) continue
    if (failedKeys.has(task.cachedKey) || collectedByRepo.has(task.cachedKey)) continue
    collectedByRepo.set(task.cachedKey, [])
  }
  flushCollected()
  persistCandidateCache()

  // 只有「这个仓库的所有扩展都跑完且没有失败」才算加载完成；取消掉的仓库保持未加载。
  const loaded = new Set(candidateLoadedRepos.value)
  for (const cachedKey of new Set(tasks.map((task) => task.cachedKey))) {
    if (failedKeys.has(cachedKey)) continue
    const repoTasks = tasks.filter((task) => task.cachedKey === cachedKey)
    if (repoTasks.every((task) => processedKeys.has(task.cachedKey))) loaded.add(cachedKey)
  }
  candidateLoadedRepos.value = loaded

  candidateLoading.value = false
  candidateProgress.label = ''
  nextTick(updateAllStripBounds)
  if (candidateCancel.value) {
    notify(`已停止加载，当前有 ${extensionCandidates.value.length} 个可添加内容源`, 'info')
  } else if (extensionCandidates.value.length) {
    notify(`已加载 ${extensionCandidates.value.length} 个可添加内容源`, 'success')
  } else if (errors.length) {
    notify(`内容源加载失败：${errors[0]}`, 'error')
  } else {
    notify('所选仓库没有可添加的内容源', 'info')
  }
}

// 枚举候选会把扩展真正装进插件运行时；关闭窗口时把没有被任何已添加来源引用的
// 插件卸掉，避免 sidecar 每次重启都加载几百个用不到的扩展。
function closeAddSource() {
  addSourceOpen.value = false
  api.prunePlugins().catch(() => {})
}

// 批量枚举要逐个安装扩展并重启插件运行时，先算清楚要装多少个，数量多就问一次。
async function startBulkCandidateLoad() {
  const targets = addRepositoryID.value === 'all'
    ? repositories.value
    : [selectedRepository.value].filter(Boolean)
  const kind = activeKind.value
  const pending = targets.reduce((sum, target) => {
    const cachedKey = `${target.id}:${kind}`
    if (candidateLoadedRepos.value.has(cachedKey)) return sum
    return sum + (target.extensions || []).filter((extension) => canInstallExtension(target, extension)).length
  }, 0)
  if (!pending) {
    notify('内容源已是最新，无需重复加载', 'info')
    return
  }
  if (pending > 5) {
    const confirmed = await askConfirm({
      title: '加载内容源',
      message: `这次需要安装并枚举 ${pending} 个扩展，每个扩展都会重启一次插件运行时，可能要等较久。确定开始吗？`,
      confirmText: '开始加载',
    })
    if (!confirmed) return
  }
  await loadRepositoryCandidates(selectedRepository.value)
}

function readCandidateCacheFromStorage() {
  try {
    const raw = window.localStorage.getItem(CANDIDATE_CACHE_KEY)
    if (!raw) return {}
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

function persistCandidateCache() {
  try {
    window.localStorage.setItem(CANDIDATE_CACHE_KEY, JSON.stringify(candidateCache.value))
  } catch {
    // 隐私模式或配额满时忽略，缓存写不进去不影响主流程。
  }
}

function withCandidateTimeout(promise) {
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(() => reject(new Error('加载超时')), CANDIDATE_TIMEOUT_MS)
    promise.then(
      (value) => {
        window.clearTimeout(timer)
        resolve(value)
      },
      (err) => {
        window.clearTimeout(timer)
        reject(err)
      },
    )
  })
}

function syncExtensionCandidates() {
  const targets = addRepositoryID.value === 'all'
    ? repositories.value
    : [selectedRepository.value].filter(Boolean)
  const seen = new Set()
  const collected = []
  for (const target of targets) {
    const rows = candidateCache.value[`${target.id}:${activeKind.value}`] || []
    for (const candidate of rows) {
      if (!candidate?.id || seen.has(candidate.id)) continue
      seen.add(candidate.id)
      collected.push(candidate)
    }
  }
  extensionCandidates.value = collected
}

function stripElement(name) {
  return name === 'repository' ? repositoryStripRef.value : typeStripRef.value
}

function updateStripBounds(name) {
  const element = stripElement(name)
  if (!element) {
    stripBounds[name].left = false
    stripBounds[name].right = false
    return
  }
  stripBounds[name].left = element.scrollLeft > 2
  stripBounds[name].right = element.scrollLeft + element.clientWidth < element.scrollWidth - 2
}

function updateAllStripBounds() {
  updateStripBounds('repository')
  updateStripBounds('type')
}

function scrollChipStrip(name, direction) {
  const element = stripElement(name)
  if (!element) return
  element.scrollBy({ left: direction * Math.max(150, element.clientWidth * 0.7), behavior: 'smooth' })
}

function handleChipStripWheel(name, event) {
  const element = stripElement(name)
  if (!element || element.scrollWidth <= element.clientWidth) return
  const delta = Math.abs(event.deltaY) >= Math.abs(event.deltaX) ? event.deltaY : event.deltaX
  if (delta === 0) return
  if (delta < 0 && element.scrollLeft <= 0) return
  if (delta > 0 && element.scrollLeft + element.clientWidth >= element.scrollWidth - 1) return
  event.preventDefault()
  element.scrollBy({ left: delta, behavior: 'auto' })
}

async function loadExtensionCandidates(repository, extension) {
  if (!canInstallExtension(repository, extension)) {
    notify('该来源是插件，但当前运行时无法执行它', 'warning')
    return
  }
  busy.value = `extension-${extension.id}`
  candidateLoading.value = true
  candidateProgress.done = 0
  candidateProgress.total = 1
  candidateProgress.label = `${repository.name} · ${extension.name || extension.id}`
  try {
    const result = await withCandidateTimeout(api.importRepositoryExtension(repository.id, extension.id))
    const candidates = Array.isArray(result.candidates) ? result.candidates : (Array.isArray(result.sources) ? result.sources : [])
    // 逐个扩展加载时只做增量合并，不把整个仓库标记为已加载，方便继续挑其它扩展。
    const cachedKey = `${repository.id}:${activeKind.value}`
    const seen = new Set()
    const rows = [
      ...(candidateCache.value[cachedKey] || []),
      ...candidates.map((candidate) => ({
        ...candidate,
        repositoryId: repository.id,
        repositoryName: repository.name,
      })),
    ]
    candidateCache.value = {
      ...candidateCache.value,
      [cachedKey]: rows.filter((candidate) => {
        if (!candidate?.id || seen.has(candidate.id)) return false
        seen.add(candidate.id)
        return true
      }),
    }
    persistCandidateCache()
    syncExtensionCandidates()
    notify(candidates.length ? `已加载 ${candidates.length} 个可添加内容源` : '该仓库没有可添加内容源', candidates.length ? 'success' : 'info')
  } catch (err) {
    notify(`加载内容源失败：${err.message}`, 'error')
  } finally {
    candidateLoading.value = false
    candidateProgress.done = candidateProgress.total
    candidateProgress.label = ''
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
    await api.saveAccount(saved.id, { useProxy: addUseProxy.value !== false }).catch(() => {})
    sources.value = [...sources.value, saved]
    extensionCandidates.value = extensionCandidates.value.filter((item) => item.id !== candidate.id)
    // 同步从本地候选缓存里移除，避免重开窗口又把已添加的来源列出来。
    const nextCache = {}
    for (const [key, rows] of Object.entries(candidateCache.value)) {
      nextCache[key] = (rows || []).filter((item) => item.id !== candidate.id)
    }
    candidateCache.value = nextCache
    persistCandidateCache()
    notify(`${saved.name} 已添加`, 'success')
  } catch (err) {
    notify(`添加失败：${err.message}`, 'error')
  } finally {
    busy.value = ''
  }
}

function onDragStart(item, event) {
	// 内置源顺序固定，不允许拖动。
	if (item?.builtin) {
		event?.preventDefault?.()
		return
	}
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
  const dragged = sources.value.find((row) => row.id === dragId.value)
  // 内置源不参与排序，插件源也不能排到内置源前面。
  if (!dragged || dragged.builtin || item.builtin) return
  if ((dragged.kind || 'comic') !== (item.kind || 'comic')) return
  event.preventDefault()
  dragOverId.value = item.id
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
  dragOverId.value = ''
  // 只持久化插件源顺序，内置源始终按注册表顺序排在最前。
  const ids = visibleSources.value.filter((item) => !item.builtin).map((item) => item.id)
  if (!ids.length) return
  try {
    // 用独立入口保存顺序：以前借某个内容源的 PATCH 夹带 order，目标源一旦
    // 查不到就整条请求 404，表现成「拖了但没生效」。
    await api.updateSourceOrder({ kind: activeKind.value, order: ids })
    notify('来源顺序已保存', 'success')
  } catch (err) {
    notify(`保存排序失败：${err.message}`, 'error')
  }
}

function onDragEnd() {
  dragId.value = ''
  dragOverId.value = ''
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
        :class="{ 'source-card-disabled': item.hidden, 'source-card-dragging': dragId === item.id, 'source-card-drop-target': dragOverId === item.id && dragId !== item.id, 'source-card-selected': isSourceSelected(item) }"
        :draggable="!item.builtin"
        @dragstart="onDragStart(item, $event)"
        @click="handleSourceCardClick(item, index, $event)"
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
          <SourceIcon v-else :src="item.icon" :kind="item.kind" :alt="item.name" :size="22" />
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
          :disabled="categoryLoading === item.id"
          @click="openCategory(item)"
        >
          <Loader2 v-if="categoryLoading === item.id" :size="17" class="spin" />
          <SlidersHorizontal v-else :size="17" />
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

        <div v-if="addSourceOpen" class="modal-backdrop" @click.self="closeAddSource">
    <div class="modal modal-lg source-add-modal">
      <div class="modal-head">
        <div>
          <h2>添加内容源</h2>
          <p class="muted small">从拓展仓库中选择要加入资源库的内容源，添加后可在探索发现中使用。</p>
        </div>
              <button class="btn ghost icon" type="button" aria-label="关闭" @click="closeAddSource">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <div class="source-add-filters">
          <div class="source-chip-strip" @mouseenter="updateStripBounds('repository')">
            <button
              class="source-chip-strip-arrow left"
              type="button"
              :class="{ visible: stripBounds.repository.left }"
              aria-label="向前滚动仓库"
              @click="scrollChipStrip('repository', -1)"
            >
              <ChevronLeft :size="15" />
            </button>
            <div
              ref="repositoryStripRef"
              class="source-chip-strip-track"
              @scroll="updateStripBounds('repository')"
              @wheel="handleChipStripWheel('repository', $event)"
            >
              <button
                v-for="option in repositoryFilterOptions"
                :key="option.value"
                class="source-chip"
                type="button"
                :class="{ active: addRepositoryID === option.value }"
                @click="addRepositoryID = option.value"
              >
                {{ option.label }}
              </button>
            </div>
            <button
              class="source-chip-strip-arrow right"
              type="button"
              :class="{ visible: stripBounds.repository.right }"
              aria-label="向后滚动仓库"
              @click="scrollChipStrip('repository', 1)"
            >
              <ChevronRight :size="15" />
            </button>
          </div>
          <div class="source-chip-strip" @mouseenter="updateStripBounds('type')">
            <button
              class="source-chip-strip-arrow left"
              type="button"
              :class="{ visible: stripBounds.type.left }"
              aria-label="向前滚动类型"
              @click="scrollChipStrip('type', -1)"
            >
              <ChevronLeft :size="15" />
            </button>
            <div
              ref="typeStripRef"
              class="source-chip-strip-track"
              @scroll="updateStripBounds('type')"
              @wheel="handleChipStripWheel('type', $event)"
            >
              <button
                v-for="option in candidateFilterOptions"
                :key="option.value"
                class="source-chip"
                type="button"
                :class="{ active: candidateFilter === option.value }"
                @click="candidateFilter = option.value"
              >
                {{ option.label }}
              </button>
            </div>
            <button
              class="source-chip-strip-arrow right"
              type="button"
              :class="{ visible: stripBounds.type.right }"
              aria-label="向后滚动类型"
              @click="scrollChipStrip('type', 1)"
            >
              <ChevronRight :size="15" />
            </button>
          </div>
        </div>
        <div class="source-add-toolbar">
          <label class="source-repository-search">
            <Search :size="15" />
            <input v-model="sourceSearch" placeholder="搜索内容源" />
          </label>
          <label class="source-proxy-toggle">
            <input v-model="addUseProxy" type="checkbox" />
            <span>应用代理</span>
          </label>
          <button
            v-if="!candidateLoading"
            class="btn small secondary"
            type="button"
            @click="startBulkCandidateLoad"
          >
            <PackagePlus :size="14" />
            {{ addRepositoryID === 'all' ? '加载全部仓库内容源' : '加载本仓库内容源' }}
          </button>
          <button v-else class="btn small secondary" type="button" @click="candidateCancel = true">
            <X :size="14" />
            取消<template v-if="candidateProgress.total">（{{ candidateProgress.done }}/{{ candidateProgress.total }}）</template>
          </button>
        </div>
        <div v-if="candidateLoading && !visibleCandidates.length" class="empty compact">
          <Loader2 :size="20" class="spin" />
          <span>
            正在加载仓库内容源<template v-if="candidateProgress.total">（{{ candidateProgress.done }}/{{ candidateProgress.total }}）</template>
            <template v-if="candidateProgress.label"> · {{ candidateProgress.label }}</template>
          </span>
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
        <div v-else-if="addRepositoryID === 'all'" class="empty compact">
          <PackagePlus :size="20" />
          <span>先选一个具体仓库，点某个拓展后面的「加载内容源」；或点上方「加载全部仓库内容源」一次性枚举。</span>
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


  <div v-if="browserOpen && browserSource" class="modal-backdrop browser-verify-backdrop" @click.self="browserOpen = false">
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
    <div class="modal modal-lg source-editor-modal">
      <div class="modal-head">
        <div class="inline">
          <SourceIcon
            class="source-editor-icon"
            :src="editorSource.icon"
            :kind="editorSource.kind"
            :alt="editorSource.name"
            :size="26"
          />
          <div>
            <div class="source-editor-name-row">
              <h2>{{ editorSource.name }}</h2>
              <span
                v-if="isPluginSource(editorSource) && editorSource.repositoryName"
                class="plugin-repo-pill"
                :title="`来自拓展仓库：${editorSource.repositoryName}`"
              >
                {{ editorSource.repositoryName }}
              </span>
            </div>
            <p class="muted small">
              {{ sourceStatus(editorSource).label }}
            </p>
          </div>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="editorOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <section class="source-editor-section">
            <label v-if="!editorSource.builtin" class="field">
              <span>内容源名称</span>
              <input v-model="forms[editorSource.id].name" class="input" placeholder="自定义显示名称" />
            </label>
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
              <div v-if="pluginPreferenceNodes().length" class="settings-grid plugin-preference-grid">
                <div v-for="node in pluginPreferenceNodes()" :key="node.id" class="field plugin-preference-row">
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
                </div>
              </div>
            </template>
          </template>
          <template v-else>
            <div class="settings-grid source-editor-pair">
              <label class="field">
                <span>网站地址（留空自动优选）</span>
                <SitePicker
                  v-model="forms[editorSource.id].homeUrl"
                  :options="editorSource.sites || []"
                  placeholder="留空自动选择，优选可用地址"
                />
              </label>
              <label v-if="isJMComic(editorSource)" class="field">
                <span>User-Agent（可选）</span>
                <input
                  v-model="forms[editorSource.id].userAgent"
                  class="input"
                  placeholder="留空使用默认浏览器标识"
                />
              </label>
              <label v-else-if="!showsAccountLogin(editorSource) && !supportsRelogin(editorSource)" class="field">
                <span>Cookie（可选，可拖动调整高度）</span>
                <textarea
                  v-model="forms[editorSource.id].cookie"
                  class="textarea cookie-textarea"
                  placeholder="粘贴浏览器中的 Cookie"
                />
              </label>
              <label v-if="supportsRelogin(editorSource)" class="field">
                <span>重新登录保活（天）</span>
                <input
                  v-model.number="forms[editorSource.id].reloginDays"
                  class="input"
                  type="number"
                  min="1"
                  max="365"
                  placeholder="默认 7 天"
                />
              </label>
            </div>
            <div v-if="showsAccountLogin(editorSource)" class="settings-grid">
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
        </section>

        <section class="source-editor-section">
          <h3>图标</h3>
          <div class="source-icon-editor">
            <span class="source-icon-preview">
              <SourceIcon :src="editorSource.icon" :kind="editorSource.kind" :alt="editorSource.name" :size="26" />
            </span>
            <div class="source-icon-actions">
              <button
                class="btn secondary small"
                type="button"
                :disabled="iconBusy === editorSource.id"
                @click="retrySourceIcon(editorSource)"
              >
                <Loader2 v-if="iconBusy === editorSource.id" :size="14" class="spin" />
                <RefreshCw v-else :size="14" />
                重新获取
              </button>
              <button
                class="btn secondary small"
                type="button"
                :disabled="iconBusy === editorSource.id"
                @click="openIconLibrary()"
              >
                <Images :size="14" />
                从图标库选择
              </button>
              <button
                class="btn secondary small"
                type="button"
                :disabled="iconBusy === editorSource.id"
                @click="resetSourceIcon(editorSource)"
              >
                <RotateCcw :size="14" />
                恢复默认
              </button>
            </div>
          </div>
          <p class="muted small">默认从内容源网站获取图标并保存到 /data/icon；获取失败时显示对应类型的默认图标。</p>
        </section>

        <section class="source-editor-section">
          <h3>下载策略</h3>
          <label class="field">
            <span>默认下载位置</span>
            <DirectoryPicker
              v-model="forms[editorSource.id].defaultDownloadDir"
              :allow-default="false"
              placeholder="选择该内容源订阅时的默认下载位置"
            />
          </label>
          <p class="muted small">从该内容源创建订阅时会默认使用这个位置，订阅时仍可手动修改。</p>
          <div class="settings-grid settings-grid-4">
            <label class="field">
              <span>章节线程</span>
          <input v-model.number="forms[editorSource.id].chapterConcurrency" class="input" type="number" min="1" max="32" />
            </label>
            <label class="field">
              <span>图片线程</span>
          <input v-model.number="forms[editorSource.id].pageConcurrency" class="input" type="number" min="1" max="32" />
            </label>
            <label class="field">
              <span>每下载 N 章暂停</span>
              <input v-model.number="forms[editorSource.id].batchSize" class="input" type="number" min="0" max="1000" />
            </label>
            <label class="field">
              <span>暂停分钟</span>
              <input v-model.number="forms[editorSource.id].batchIntervalMinutes" class="input" type="number" min="0" max="10080" />
            </label>
          </div>
          <p class="muted small">留空或填 0 表示不启用批量暂停。</p>
        </section>

        <section class="source-editor-section">
          <h3>网络</h3>
          <label class="switch-row">
            <input v-model="forms[editorSource.id].useProxy" type="checkbox" />
            <span>应用代理</span>
          </label>
          <p class="muted small">关闭后该内容源将直连，不经过系统设置中的全局代理。</p>
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
          v-if="showsAccountLogin(editorSource)"
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

  <div v-if="iconLibraryOpen" class="modal-backdrop" @click.self="iconLibraryOpen = false">
    <div class="modal icon-library-modal">
      <div class="modal-head">
        <div>
          <h2>本地图标库</h2>
          <p class="muted small">图标保存在容器 /data/icon 目录</p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="iconLibraryOpen = false">
          <X :size="18" />
        </button>
      </div>
      <input
        ref="iconUploadInput"
        class="hidden-file-input"
        type="file"
        accept="image/png,image/jpeg,image/webp,image/x-icon,image/vnd.microsoft.icon,.png,.jpg,.jpeg,.webp,.ico"
        @change="uploadIconFile"
      />
      <div class="modal-body">
        <div class="icon-library-tools">
          <div class="icon-library-url">
            <button class="btn secondary small icon-upload-button" type="button" :disabled="iconUploading" @click="iconUploadInput?.click()">
              <Loader2 v-if="iconUploading" :size="14" class="spin" />
              <Upload v-else :size="14" />
              上传图标
            </button>
            <input
              v-model="iconUrlInput"
              class="input"
              placeholder="输入图标网址，例如 https://bica.mom/favicon.jpeg"
              @keyup.enter="downloadIconFromUrl"
            />
            <button class="btn small" type="button" :disabled="iconDownloading" @click="downloadIconFromUrl">
              <Loader2 v-if="iconDownloading" :size="14" class="spin" />
              <Download v-else :size="14" />
              下载图标
            </button>
          </div>
          <label class="icon-library-search">
            <Search :size="15" />
            <input v-model="iconLibraryQuery" class="input" placeholder="搜索图标" />
            <button v-if="iconLibraryQuery" class="icon-btn" type="button" aria-label="清除搜索" @click="iconLibraryQuery = ''">
              <X :size="14" />
            </button>
          </label>
        </div>
        <div v-if="!iconLibrary.length" class="empty">
          <Images :size="24" />
          <p>图标库为空。可先对内容源执行“重新获取”生成图标，或直接上传本地图片。</p>
        </div>
        <div v-else-if="!filteredIconLibrary.length" class="empty">
          <Search :size="22" />
          <p>没有匹配“{{ iconLibraryQuery }}”的图标。</p>
        </div>
        <div v-else class="icon-library-grid">
          <button
            v-for="icon in filteredIconLibrary"
            :key="icon.id"
            class="icon-library-item"
            type="button"
            :disabled="iconBusy === editorSource?.id"
            @click="chooseSourceIcon(icon)"
          >
            <span class="icon-library-thumb">
              <img :src="icon.url" :alt="icon.name || icon.id" loading="lazy" />
            </span>
            <span class="icon-library-name">{{ icon.name || icon.id }}</span>
          </button>
        </div>
      </div>
    </div>
  </div>

  <div v-if="repositoryOpen" class="modal-backdrop" @click.self="repositoryOpen = false">
    <div class="modal">
      <div class="modal-head">
        <div>
          <h2>{{ editingRepositoryId ? '编辑拓展仓库' : '添加拓展仓库' }}</h2>
          <p class="muted small">{{ editingRepositoryId ? '修改仓库名称、地址或类型，保存后会重新同步来源。' : '添加兼容 Kototoro、Mihon、JAR 等格式的仓库清单地址，仓库名称留空时自动使用清单名称。' }}</p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="repositoryOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <label class="field">
          <span>仓库名称（留空自动获取）</span>
          <input v-model="repositoryForm.name" class="input" placeholder="留空由仓库清单自动命名；填写后显示自定义名称" />
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
