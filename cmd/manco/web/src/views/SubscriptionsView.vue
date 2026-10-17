<script setup>
import { computed, onDeactivated, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  Archive,
  BookOpen,
  CalendarClock,
  Film,
  FolderOpen,
  History,
  Layers,
  Layers3,
  ListChecks,
  Loader2,
  MoreVertical,
  Pencil,
  RefreshCcw,
  RefreshCw,
  Rss,
  Save,
  Sparkles,
  Trash2,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import ContentLink from '../components/ContentLink.vue'
import FunctionOption from '../components/FunctionOption.vue'
import SortMenu from '../components/SortMenu.vue'
import DirectoryPicker from '../components/DirectoryPicker.vue'
import SourceIcon from '../components/SourceIcon.vue'
import { askConfirm } from '../stores/confirm'
import { notify } from '../stores/notices'
import { rememberSubscriptionDownloadDir } from '../stores/subscriptionPreferences'

const items = ref([])
const jobs = ref([])
const sources = ref({})
const jobStatsData = ref({})
const kindFilter = ref('all')
const activeDir = ref('')
const loading = ref(true)
const busy = ref(0)
const batchBusy = ref(false)
const openMenu = ref(0)
// 三点菜单的弹出方向：上方空间够就向上，否则向下。
const menuPlacement = ref('up')
const editorOpen = ref(false)
const editing = ref(null)
const selectionMode = ref(false)
const selected = ref(new Set())
// 订阅很多时逐个渲染所有卡片会把主线程拖到无响应，这里分批渲染。
const renderLimit = ref(60)
// 订阅清单分页：首屏只取一页，滚到底再补。
const SUBS_PAGE_SIZE = 60
const totalSubscriptions = ref(0)
const loadingMoreSubscriptions = ref(false)
let scrollEl = null
// 5 秒轮询时如果内容没变就不替换响应式数据，避免整页卡片反复重渲染。
let lastItemsSignature = ''
let lastStatsSignature = ''
let lastJobsSignature = ''
let selectAnchor = -1
let timer = 0

const form = ref({
  cronExpr: '',
  downloadDir: '',
  seriesDir: '',
  convertToSimplified: true,
  chapterMarker: false,
  coverBlur: false,
})

// 有排队/进行中的章节时才轮询刷新；数量由 /api/subscriptions 的 jobStats 给出。
const hasActive = computed(() =>
  Object.values(jobStatsData.value || {}).some((value) => Number(value?.pending || 0) > 0),
)

function itemKind(item) {
  const kind = sources.value[item.sourceId]?.kind
  return kind === 'book' || kind === 'other' ? kind : 'comic'
}

const downloadDirs = computed(() => {
  const seen = new Set()
  const dirs = []
  for (const item of items.value) {
    const dir = String(item.downloadDir || '').trim()
    if (!dir || seen.has(dir)) continue
    seen.add(dir)
    dirs.push(dir)
  }
  return dirs
})

function dirLabel(dir) {
  const cleaned = String(dir || '').replace(/[\\/]+$/, '')
  const parts = cleaned.split(/[\\/]/).filter(Boolean)
  return parts.length ? parts[parts.length - 1] : cleaned
}

// 二级排序：默认按执行时间降序。选择持久化在 localStorage。
const SORT_STORAGE_KEY = 'manco.subscriptions.sort.v1'
const sortOptions = [
  { value: 'executed', label: '执行时间' },
  { value: 'created', label: '添加时间' },
  { value: 'title', label: '标题' },
]
const sortMode = ref('executed')
const sortDesc = ref(true)
try {
  const saved = JSON.parse(window.localStorage.getItem(SORT_STORAGE_KEY) || 'null')
  if (saved && sortOptions.some((option) => option.value === saved.mode)) sortMode.value = saved.mode
  if (saved && typeof saved.desc === 'boolean') sortDesc.value = saved.desc
} catch {
  // 忽略损坏的本地设置
}
watch([sortMode, sortDesc], () => {
  try {
    window.localStorage.setItem(SORT_STORAGE_KEY, JSON.stringify({ mode: sortMode.value, desc: sortDesc.value }))
  } catch {
    // 隐私模式下写不进去，忽略
  }
})

function onSortChange({ mode, desc }) {
  sortMode.value = mode
  sortDesc.value = desc
}

const sortedItems = computed(() => {
  const direction = sortDesc.value ? -1 : 1
  const rows = [...items.value]
  rows.sort((a, b) => {
    let left
    let right
    if (sortMode.value === 'title') {
      left = String(a.title || '')
      right = String(b.title || '')
    } else if (sortMode.value === 'created') {
      left = String(a.createdAt || '')
      right = String(b.createdAt || '')
    } else {
      left = String(a.updatedAt || '')
      right = String(b.updatedAt || '')
    }
    if (left === right) return 0
    return (left > right ? 1 : -1) * direction
  })
  return rows
})

const filteredItems = computed(() => {
  let rows = sortedItems.value
  if (kindFilter.value !== 'all') rows = rows.filter((item) => itemKind(item) === kindFilter.value)
  if (activeDir.value) rows = rows.filter((item) => String(item.downloadDir || '').trim() === activeDir.value)
  return rows
})

const renderedItems = computed(() => filteredItems.value.slice(0, renderLimit.value))

watch([kindFilter, activeDir], () => {
  renderLimit.value = 60
})

function handlePageScroll() {
  if (!scrollEl) return
  if (scrollEl.scrollTop + scrollEl.clientHeight >= scrollEl.scrollHeight - 360) {
    // 先补数据页，再扩渲染窗口。
    if (items.value.length < totalSubscriptions.value) loadMoreSubscriptions()
    if (renderLimit.value < filteredItems.value.length) renderLimit.value += 60
  }
}

async function loadMoreSubscriptions() {
  if (loadingMoreSubscriptions.value || items.value.length >= totalSubscriptions.value) return
  loadingMoreSubscriptions.value = true
  try {
    const payload = await api.subscriptions(SUBS_PAGE_SIZE, items.value.length)
    const next = payload.items || []
    if (next.length) items.value = [...items.value, ...next]
    totalSubscriptions.value = Number(payload.total ?? totalSubscriptions.value)
    if (payload.jobStats) jobStatsData.value = { ...jobStatsData.value, ...payload.jobStats }
  } catch (err) {
    notify(`加载更多订阅失败：${err.message}`, 'error')
  } finally {
    loadingMoreSubscriptions.value = false
  }
}

const selectedItems = computed(() => items.value.filter((item) => selected.value.has(item.id)))
const selectedCount = computed(() => selectedItems.value.length)

// 任务统计预先聚合成 map：模板里每张卡片只做一次 O(1) 查表，避免
// “卡片数 × 任务数”的重复扫描把主线程拖死。
const statsByComic = computed(() => {
  const map = new Map()
  for (const [key, value] of Object.entries(jobStatsData.value || {})) {
    map.set(key, {
      success: Number(value?.succeeded || 0),
      failed: Number(value?.failed || 0),
    })
  }
  if (!map.size) {
    for (const job of jobs.value) {
      const key = `${job.sourceId}\u0000${job.comicId}`
      const entry = map.get(key) || { success: 0, failed: 0 }
      if (job.status === 'completed') entry.success += 1
      else if (job.status === 'failed') entry.failed += 1
      map.set(key, entry)
    }
  }
  return map
})
const allSelected = computed(
  () => filteredItems.value.length > 0 && filteredItems.value.every((item) => selected.value.has(item.id)),
)

function isSubSelected(item) {
  return selected.value.has(item.id)
}

function enterSelection(item, index, event) {
  event?.preventDefault?.()
  const next = new Set(selected.value)
  if (event?.shiftKey && selectAnchor >= 0) {
    const from = Math.min(selectAnchor, index)
    const to = Math.max(selectAnchor, index)
    filteredItems.value.slice(from, to + 1).forEach((row) => next.add(row.id))
  } else {
    next.add(item.id)
  }
  selected.value = next
  selectionMode.value = true
  selectAnchor = index
}

function handleCardClick(item, index, event) {
  if (event?.shiftKey) event.preventDefault()
  const next = new Set(selected.value)
  if (event?.shiftKey && selectAnchor >= 0) {
    const from = Math.min(selectAnchor, index)
    const to = Math.max(selectAnchor, index)
    filteredItems.value.slice(from, to + 1).forEach((row) => next.add(row.id))
  } else if (next.has(item.id)) {
    next.delete(item.id)
  } else {
    next.add(item.id)
  }
  selected.value = next
  selectionMode.value = next.size > 0
  selectAnchor = index
}

// 选中态下用捕获阶段拦截卡片内部链接/按钮的点击，改为切换选中。
function captureCardClick(item, index, event) {
  if (!selectionMode.value) return
  event.preventDefault()
  event.stopPropagation()
  handleCardClick(item, index, event)
}

function selectAllSubscriptions() {
  selected.value = new Set(filteredItems.value.map((item) => item.id))
  selectionMode.value = selected.value.size > 0
  selectAnchor = selected.value.size ? 0 : -1
}

function clearSubscriptionSelection() {
  selected.value = new Set()
  selectionMode.value = false
  selectAnchor = -1
}

function kindCount(kind) {
  if (kind === 'all') return items.value.length
  return items.value.filter((item) => itemKind(item) === kind).length
}

onMounted(async () => {
  await load()
  scrollEl = document.querySelector('.content-scroll')
  scrollEl?.addEventListener('scroll', handlePageScroll, { passive: true })
  timer = window.setInterval(() => {
    if (hasActive.value) load(true)
  }, 5000)
  window.addEventListener('click', closeMenus)
  window.addEventListener('keydown', onKeydown)
})

onUnmounted(() => {
  if (timer) window.clearInterval(timer)
  scrollEl?.removeEventListener('scroll', handlePageScroll)
  window.removeEventListener('click', closeMenus)
  window.removeEventListener('keydown', onKeydown)
})

onDeactivated(() => {
  clearSubscriptionSelection()
})

function onKeydown(event) {
  if (event.key === 'Escape' && selectionMode.value) clearSubscriptionSelection()
}

function closeMenus(event) {
  if (event && event.target.closest('.subscription-menu')) return
  openMenu.value = 0
}

async function load(silent = false) {
  if (!silent) loading.value = true
  try {
    const [subscriptions, downloads, sourcePayload] = await Promise.all([
      api.subscriptions(silent ? Math.max(SUBS_PAGE_SIZE, items.value.length) : SUBS_PAGE_SIZE, 0),
      // 统计信息由 /api/subscriptions 的 jobStats 直接给出，不再额外拉一次
      // 几百条下载任务——那个请求服务端每条都要查本地封面与元数据，5 秒
      // 轮询一次会把机器拖住。
      Promise.resolve({ items: [] }),
      Object.keys(sources.value).length ? Promise.resolve({ items: [] }) : api.sources().catch(() => ({ items: [] })),
    ])
    for (const source of sourcePayload.items || []) {
      sources.value = { ...sources.value, [source.id]: source }
    }
    const nextItems = subscriptions.items || []
    totalSubscriptions.value = Number(subscriptions.total || nextItems.length)
    const itemsSignature = JSON.stringify(
      nextItems.map((item) => [
        item.id,
        item.enabled,
        item.coverBlur,
        item.lastChapterTitle,
        item.cronExpr,
        item.downloadDir,
        item.seriesDir,
        item.autoDownload,
      ]),
    )
    if (!silent || itemsSignature !== lastItemsSignature) {
      items.value = nextItems
      lastItemsSignature = itemsSignature
    }
    const nextStats = subscriptions.jobStats || {}
    const statsSignature = JSON.stringify(nextStats)
    if (!silent || statsSignature !== lastStatsSignature) {
      jobStatsData.value = nextStats
      lastStatsSignature = statsSignature
    }
    const nextJobs = downloads.items || []
    const jobsSignature = JSON.stringify(nextJobs.map((job) => [job.id, job.status]))
    if (!silent || jobsSignature !== lastJobsSignature) {
      jobs.value = nextJobs
      lastJobsSignature = jobsSignature
    }
    const available = new Set(items.value.map((item) => item.id))
    if ([...selected.value].some((id) => !available.has(id))) {
      selected.value = new Set([...selected.value].filter((id) => available.has(id)))
      if (!selected.value.size) selectionMode.value = false
    }
  } catch (err) {
    if (!silent) notify(`订阅清单加载失败：${err.message}`, 'error')
  } finally {
    loading.value = false
  }
}

function toggleMenu(item, event) {
  event.stopPropagation()
  if (openMenu.value === item.id) {
    openMenu.value = 0
    return
  }
  // 菜单大约 240px 高：三点上方空间够就向上弹，否则向下弹，避免被页面顶部裁掉。
  const trigger = event?.currentTarget?.getBoundingClientRect?.()
  const spaceAbove = trigger ? trigger.top : window.innerHeight
  menuPlacement.value = spaceAbove > 260 ? 'up' : 'down'
  openMenu.value = item.id
}

function openEditor(item) {
  editing.value = item
  form.value = {
    cronExpr: item.cronExpr || '',
    downloadDir: item.downloadDir || '',
    seriesDir: item.seriesDir || '',
    convertToSimplified: Boolean(item.convertToSimplified),
    chapterMarker: Boolean(item.chapterMarker),
    coverBlur: Boolean(item.coverBlur),
  }
  openMenu.value = 0
  editorOpen.value = true
}

async function saveEditor() {
  if (!editing.value) return
  // Cron 留空表示这个订阅不做定时检查，不再强制填写。
  if (!form.value.downloadDir.trim()) {
    notify('请选择下载位置', 'warning')
    return
  }
  const enablingMarker = Boolean(form.value.chapterMarker) && !editing.value.chapterMarker
  let markerResult = null
  if (enablingMarker) {
    const confirmed = await askConfirm({
      title: '补全章节标符',
      message: '开启后会把该订阅已下载的章节文件名补全为 #序号 章节名，是否继续？',
      confirmText: '确认补全',
    })
    if (!confirmed) return
  }
  busy.value = editing.value.id
  try {
    const updated = await api.updateSubscription(editing.value.id, {
      cronExpr: form.value.cronExpr.trim(),
      downloadDir: form.value.downloadDir.trim(),
      seriesDir: form.value.seriesDir.trim(),
      convertToSimplified: Boolean(form.value.convertToSimplified),
      chapterMarker: Boolean(form.value.chapterMarker),
      coverBlur: Boolean(form.value.coverBlur),
    })
    if (enablingMarker) markerResult = await api.backfillChapterMarkers(editing.value.id)
    Object.assign(editing.value, updated)
    rememberSubscriptionDownloadDir(updated.downloadDir)
    if (enablingMarker) {
      const renamed = Number(markerResult?.renamed || 0)
      const failed = Number(markerResult?.failed || 0)
      notify(`已更新《${editing.value.title}》，补全 ${renamed} 个章节标符${failed ? `，${failed} 个失败` : ''}`, failed ? 'warning' : 'success')
    } else {
      notify(`已更新《${editing.value.title}》的订阅任务`, 'success')
    }
    editorOpen.value = false
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = 0
  }
}

async function toggleEnabled(item) {
  openMenu.value = 0
  busy.value = item.id
  try {
    const updated = await api.updateSubscription(item.id, { enabled: !item.enabled })
    Object.assign(item, updated)
    notify(`《${item.title}》已${item.enabled ? '启用' : '禁用'}`, 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = 0
  }
}

async function archive(item) {
  openMenu.value = 0
  busy.value = item.id
  try {
    await api.archiveSubscription(item.id)
    items.value = items.value.filter((row) => row.id !== item.id)
    notify(`《${item.title}》已归档`, 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = 0
  }
}

async function remove(item) {
  openMenu.value = 0
  const confirmed = await askConfirm({
    title: '删除订阅',
    message: `确定删除订阅「${item.title}」吗？还在下载和排队的任务会被取消，已下载的文件会保留。`,
    confirmText: '删除',
    danger: true,
  })
  if (!confirmed) return
  busy.value = item.id
  try {
    await api.deleteSubscription(item.id)
    items.value = items.value.filter((row) => row.id !== item.id)
    notify(`《${item.title}》已删除`, 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = 0
  }
}

function cover(item) {
  if (item.localCover) return api.localFileUrl(item.localCover, false, 320)
  if (item.cachedCover) return item.cachedCover
  return api.imageUrl(item.cover, item.sourceId, '', 320)
}

function sourceIcon(item) {
  return sources.value[item.sourceId]?.icon || ''
}

function sourceLabel(item) {
  return sources.value[item.sourceId]?.name || item.sourceId
}

function chapterCount(item) {
  if (item.chapterCount) return item.chapterCount
  return jobs.value.filter((job) => job.sourceId === item.sourceId && job.comicId === item.comicId).length
}

// 标题不在 JS 里按字符数截断，交给卡片宽度和 CSS 省略号处理。
function cardTitle(item) {
  return String(item?.title || '').trim() || '未命名作品'
}

function jobStats(item) {
  const key = `${item.sourceId}\u0000${item.comicId}`
  return statsByComic.value.get(key) || { success: 0, failed: 0 }
}

// 卡片里的「最近执行」＝上一次定时检查/手动检查的时间，后端存的是 UTC。
function lastRunLabel(item) {
  const raw = item?.lastCheckedAt
  if (!raw) return '最近执行：尚未执行'
  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) return '最近执行：尚未执行'
  const pad = (value) => String(value).padStart(2, '0')
  // 只显示「月/日 时:分」，例如 10/17 12:42。
  return `最近执行：${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function downloadTaskLink(item, filter) {
  if (filter === 'completed') {
    return {
      name: 'completed',
      query: { q: item.title || '' },
    }
  }
  return {
    name: 'downloads',
    query: {
      filter,
      q: item.title || '',
    },
  }
}

async function checkUpdate(item) {
  busy.value = item.id
  try {
    const result = await api.checkSubscription(item.id)
    if (result?.item) Object.assign(item, result.item)
    const retried = Number(result?.retried || 0)
    const parts = ['已检查更新']
    if (retried > 0) parts.push(`重试 ${retried} 个失败章节`)
    if (result?.completed) parts.push('作品已完结')
    notify(`《${item.title}》${parts.join('，')}`, result?.completed ? 'info' : 'success')
    await load(true)
  } catch (err) {
    notify(`检查更新失败：${err.message}`, 'error')
  } finally {
    busy.value = 0
  }
}

// 补全：把内容源章节与已完成下载比对，只把缺失的章节加入队列。
async function fillMissing(item) {
  openMenu.value = 0
  busy.value = item.id
  try {
    const result = await api.fillSubscription(item.id)
    const missing = Number(result?.missing ?? result?.queued ?? 0)
    if (missing <= 0) {
      notify(`《${item.title}》已经完整`, 'success')
    } else {
      notify(`《${item.title}》缺 ${missing} 章，已加入下载队列`, 'warning')
    }
    await load(true)
  } catch (err) {
    notify(`补全失败：${err.message}`, 'error')
  } finally {
    busy.value = 0
  }
}

async function downloadAll(item) {
  openMenu.value = 0
  const confirmed = await askConfirm({
    title: '全量下载',
    message: `确定对《${item.title}》执行全量下载吗？会删除成功与失败记录、删除作品目录里已存在的章节文件，然后重新拉取并覆盖。`,
    confirmText: '全量下载',
    danger: true,
  })
  if (!confirmed) return
  busy.value = item.id
  try {
    const result = await api.downloadAllSubscription(item.id)
    const queued = Number(result?.queued || 0)
    const removedFiles = Number(result?.removedFiles || 0)
    notify(
      [`《${item.title}》已清理旧记录并重新入队 ${queued} 个章节`, removedFiles ? `删除旧章节文件 ${removedFiles} 个` : '']
        .filter(Boolean)
        .join('，'),
      'success',
    )
    await load(true)
  } catch (err) {
    notify(`全量下载失败：${err.message}`, 'error')
  } finally {
    busy.value = 0
  }
}

// 批量操作统一走逐项接口，先二次确认再顺序执行，避免并发冲击内容源。
async function runBatch({ title, message, confirmText, danger, action, successText }) {
  const rows = selectedItems.value.slice()
  if (!rows.length || batchBusy.value) return
  const confirmed = await askConfirm({ title, message, confirmText, danger })
  if (!confirmed) return
  batchBusy.value = true
  let done = 0
  let failed = 0
  try {
    for (const item of rows) {
      try {
        await action(item)
        done += 1
      } catch {
        failed += 1
      }
    }
  } finally {
    batchBusy.value = false
  }
  notify(failed ? `${successText}：成功 ${done} 项，失败 ${failed} 项` : `${successText}：共 ${done} 项`, failed ? 'warning' : 'success')
  await load(true)
}

function batchCheck() {
  return runBatch({
    title: '检查更新',
    message: `确定对所选 ${selectedCount.value} 个订阅执行检查更新吗？`,
    confirmText: '开始检查',
    action: (item) => api.checkSubscription(item.id),
    successText: '检查更新完成',
  })
}

function batchDownloadAll() {
  return runBatch({
    title: '全量下载',
    message: `确定对所选 ${selectedCount.value} 个订阅执行全量下载吗？会删除成功与失败记录、删除作品目录里已存在的章节文件，然后重新拉取并覆盖。`,
    confirmText: '开始下载',
    action: (item) => api.downloadAllSubscription(item.id),
    successText: '全量下载已入队',
  })
}

function batchFill() {
  return runBatch({
    title: '补全缺失',
    message: `确定检查所选 ${selectedCount.value} 个订阅的缺失章节并补全下载吗？`,
    confirmText: '开始补全',
    action: (item) => api.fillSubscription(item.id),
    successText: '补全完成',
  })
}

// 刮削：重新拉取订阅源的元数据，覆盖本地已有的 ComicInfo.xml / series.json / cover。
function batchScrape() {
  return runBatch({
    title: '刮削元数据',
    message: `确定重新拉取所选 ${selectedCount.value} 个订阅的元数据，并覆盖本地已有的 ComicInfo.xml 与 series.json 吗？`,
    confirmText: '开始刮削',
    action: (item) => api.scrapeSubscription(item.id),
    successText: '刮削完成',
  })
}

function batchArchive() {
  return runBatch({
    title: '归档订阅',
    message: `确定归档所选 ${selectedCount.value} 个订阅吗？归档后可在归档集中查看。`,
    confirmText: '归档',
    action: (item) => api.archiveSubscription(item.id),
    successText: '归档完成',
  })
}

function batchDelete() {
  return runBatch({
    title: '删除订阅',
    message: `确定删除所选 ${selectedCount.value} 个订阅吗？还在下载和排队的任务会被取消，已下载的文件会保留。`,
    confirmText: '删除',
    danger: true,
    action: (item) => api.deleteSubscription(item.id),
    successText: '删除完成',
  })
}
</script>

<template>
  <div class="toolbar subscription-toolbar">
    <div class="segmented subscription-kind-tabs" role="tablist" aria-label="订阅来源类型">
      <button type="button" :class="{ active: kindFilter === 'all' }" @click="kindFilter = 'all'">
        <Layers3 :size="16" />
        全部源 <span class="segmented-count">{{ kindCount('all') }}</span>
      </button>
      <button type="button" :class="{ active: kindFilter === 'comic' }" @click="kindFilter = 'comic'">
        <BookOpen :size="16" />
        漫画源 <span class="segmented-count">{{ kindCount('comic') }}</span>
      </button>
      <button type="button" :class="{ active: kindFilter === 'book' }" @click="kindFilter = 'book'">
        <BookOpen :size="16" />
        书籍源 <span class="segmented-count">{{ kindCount('book') }}</span>
      </button>
      <button type="button" :class="{ active: kindFilter === 'other' }" @click="kindFilter = 'other'">
        <Film :size="16" />
        其他源 <span class="segmented-count">{{ kindCount('other') }}</span>
      </button>
    </div>
    <span class="spacer" />
    <button class="btn secondary small" type="button" :disabled="loading" @click="load()">
      <RefreshCw :size="15" :class="{ spin: loading }" />
      刷新
    </button>
        <ContentLink class="btn secondary small archive-entry" to="/archive">
      <Archive :size="15" />
      归档集
        </ContentLink>
  </div>

  <div v-if="downloadDirs.length" class="local-root-row">
    <div class="local-root-strip" aria-label="订阅目录">
      <button type="button" :class="{ active: !activeDir }" @click="activeDir = ''">全部</button>
      <button
        v-for="dir in downloadDirs"
        :key="dir"
        type="button"
        :class="{ active: activeDir === dir }"
        :title="dir"
        @click="activeDir = dir"
      >
        {{ dirLabel(dir) }}
      </button>
    </div>
    <SortMenu
      class="local-library-sort"
      :options="sortOptions"
      :mode="sortMode"
      :desc="sortDesc"
      @change="onSortChange"
    />
  </div>

  <div v-if="loading && !items.length" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载订阅清单</span>
  </div>
  <div v-else-if="!items.length" class="card empty">
    <Rss :size="26" />
    <p>还没有订阅。在探索发现打开作品后即可订阅追更。</p>
        <ContentLink class="btn small" to="/discover">去探索发现</ContentLink>
  </div>
  <div v-else-if="!filteredItems.length" class="card empty">
    <p>当前栏目下没有订阅。</p>
  </div>

  <div v-else class="subscription-grid">
    <article
      v-for="(item, index) in renderedItems"
      :key="item.id"
      class="subscription-card"
      :class="{
        disabled: !item.enabled,
        'menu-open': openMenu === item.id,
        selected: isSubSelected(item),
        selecting: selectionMode,
      }"
      @contextmenu.prevent="enterSelection(item, index, $event)"
      @click.capture="captureCardClick(item, index, $event)"
    >
      <button
        class="subscription-source"
        :class="{ 'is-off': !item.enabled }"
        type="button"
        :disabled="busy === item.id"
        :aria-label="item.enabled ? `禁用《${item.title}》` : `启用《${item.title}》`"
        @click.stop="toggleEnabled(item)"
      >
        <SourceIcon :src="sourceIcon(item)" :kind="itemKind(item)" :alt="item.sourceId" :size="22" />
      </button>
      <div class="subscription-main">
        <ContentLink
          class="subscription-cover"
          :class="{ blurred: item.coverBlur }"
          :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.comicId } }"
        >
          <img v-if="cover(item)" :src="cover(item)" :alt="item.title" loading="lazy" decoding="async" />
          <BookOpen v-else :size="24" />
        </ContentLink>
        <div class="subscription-meta">
          <div class="subscription-title-row">
            <ContentLink
              class="subscription-title"
              :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.comicId } }"
              :title="item.title"
            >
              {{ cardTitle(item) }}
            </ContentLink>
          </div>
          <span class="subscription-sub">{{ item.author || '未知作者' }}</span>
          <span class="subscription-sub">
            <template v-if="chapterCount(item) > 0">共 {{ chapterCount(item) }} 话 · </template>最新章节：{{ item.lastChapterTitle || '尚未记录' }}
          </span>
          <div class="subscription-line">
            <CalendarClock :size="14" />
            <span class="mono">{{ item.cronExpr || '未设置' }}</span>
          </div>
          <div class="subscription-line">
            <FolderOpen :size="14" />
            <span>{{ item.downloadDir || '未设置下载目录' }}</span>
          </div>
          <div class="subscription-line">
            <History :size="14" />
            <span>{{ lastRunLabel(item) }}</span>
          </div>
          <div class="subscription-job-stats">
            <ContentLink class="subscription-stat success" :to="downloadTaskLink(item, 'completed')">
              成功 {{ jobStats(item).success }}
            </ContentLink>
            <ContentLink class="subscription-stat failed" :to="downloadTaskLink(item, 'failed')">
              失败 {{ jobStats(item).failed }}
            </ContentLink>
          </div>
        </div>
      </div>
      <div class="subscription-tools">
        <button class="btn ghost icon" type="button" aria-label="检查更新" @click="checkUpdate(item)">
          <Loader2 v-if="busy === item.id" :size="18" class="spin" />
          <RefreshCcw v-else :size="18" />
        </button>
        <div class="subscription-menu">
          <button class="btn ghost icon subscription-more" type="button" aria-label="更多操作" @click="toggleMenu(item, $event)">
            <MoreVertical :size="19" />
          </button>
          <div
            v-if="openMenu === item.id"
            class="dropdown-panel subscription-actions"
            :class="{ 'opens-down': menuPlacement === 'down' }"
            @click.stop
          >
            <button class="dropdown-item" type="button" @click="openEditor(item)">
              <Pencil :size="15" /> 编辑
            </button>
            <button class="dropdown-item" type="button" @click="downloadAll(item)">
              <Layers :size="15" /> 全量
            </button>
            <button class="dropdown-item" type="button" @click="fillMissing(item)">
              <ListChecks :size="15" /> 补全
            </button>
            <button class="dropdown-item" type="button" @click="archive(item)">
              <Archive :size="15" /> 归档
            </button>
            <button class="dropdown-item danger" type="button" @click="remove(item)">
              <Trash2 :size="15" /> 删除
            </button>
          </div>
        </div>
      </div>
    </article>
  </div>

  <div v-if="editorOpen" class="modal-backdrop" @click.self="editorOpen = false">
    <div class="modal modal-lg subscription-modal">
      <div class="modal-head">
        <div>
          <h2>订阅任务设置</h2>
          <p class="muted small">{{ editing?.title }}</p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="editorOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <label class="field">
          <span>Cron 表达式</span>
          <input v-model="form.cronExpr" class="input" placeholder="0 21 * * 5" />
          <span class="muted small">留空即不启用定时检查更新。</span>
        </label>
        <label class="field">
          <span>下载位置</span>
          <DirectoryPicker
            v-model="form.downloadDir"
            v-model:suffix-value="form.seriesDir"
            :suffix="editing?.title"
            :convert-to-simplified="form.convertToSimplified"
            :allow-default="false"
            placeholder="选择下载位置"
          />
        </label>
        <div class="function-option-grid">
          <FunctionOption label="章节标符" :active="form.chapterMarker" @toggle="form.chapterMarker = !form.chapterMarker" />
          <FunctionOption label="封面模糊" :active="form.coverBlur" @toggle="form.coverBlur = !form.coverBlur" />
          <FunctionOption label="繁转简" :active="form.convertToSimplified" @toggle="form.convertToSimplified = !form.convertToSimplified" />
        </div>
      </div>
      <div class="modal-foot">
        <button class="btn secondary" type="button" @click="editorOpen = false">取消</button>
        <button class="btn" type="button" :disabled="busy === editing?.id" @click="saveEditor">
          <Loader2 v-if="busy === editing?.id" :size="15" class="spin" />
          <Save v-else :size="15" />
          保存
        </button>
      </div>
    </div>
  </div>

  <div v-if="selectionMode" class="selection-pill selection-pill-flat">
    <span class="selection-count">已选 {{ selectedCount }} 项</span>
    <button class="btn ghost small" type="button" :disabled="batchBusy" @click="allSelected ? clearSubscriptionSelection() : selectAllSubscriptions()">
      {{ allSelected ? '取消全选' : '全选' }}
    </button>
    <button class="btn ghost small" type="button" :disabled="batchBusy" @click="clearSubscriptionSelection">
      <X :size="14" />
      清空
    </button>
    <button class="btn ghost small" type="button" :disabled="batchBusy || !selectedCount" @click="batchCheck">
      <RefreshCcw :size="14" />
      检查
    </button>
    <button class="btn ghost small" type="button" :disabled="batchBusy || !selectedCount" @click="batchDownloadAll">
      <Layers :size="14" />
      全量
    </button>
    <button class="btn ghost small" type="button" :disabled="batchBusy || !selectedCount" @click="batchFill">
      <ListChecks :size="14" />
      补全
    </button>
    <button class="btn ghost small" type="button" :disabled="batchBusy || !selectedCount" @click="batchScrape">
      <Sparkles :size="14" />
      刮削
    </button>
    <button class="btn ghost small" type="button" :disabled="batchBusy || !selectedCount" @click="batchArchive">
      <Archive :size="14" />
      归档
    </button>
    <button class="btn ghost small danger" type="button" :disabled="batchBusy || !selectedCount" @click="batchDelete">
      <Trash2 :size="14" />
      删除
    </button>
  </div>
</template>
