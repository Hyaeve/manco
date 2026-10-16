<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  Archive,
  BookOpen,
  CalendarClock,
  Film,
  FolderOpen,
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
  Trash2,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import ContentLink from '../components/ContentLink.vue'
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
const editorOpen = ref(false)
const editing = ref(null)
const selectionMode = ref(false)
const selected = ref(new Set())
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

const hasActive = computed(() => jobs.value.some((job) => ['queued', 'running', 'paused'].includes(job.status)))

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

const filteredItems = computed(() => {
  let rows = items.value
  if (kindFilter.value !== 'all') rows = rows.filter((item) => itemKind(item) === kindFilter.value)
  if (activeDir.value) rows = rows.filter((item) => String(item.downloadDir || '').trim() === activeDir.value)
  return rows
})

const selectedItems = computed(() => items.value.filter((item) => selected.value.has(item.id)))
const selectedCount = computed(() => selectedItems.value.length)
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
  timer = window.setInterval(() => {
    if (hasActive.value) load(true)
  }, 5000)
  window.addEventListener('click', closeMenus)
  window.addEventListener('keydown', onKeydown)
})

onUnmounted(() => {
  if (timer) window.clearInterval(timer)
  window.removeEventListener('click', closeMenus)
  window.removeEventListener('keydown', onKeydown)
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
      api.subscriptions(),
      api.downloads(500),
      Object.keys(sources.value).length ? Promise.resolve({ items: [] }) : api.sources().catch(() => ({ items: [] })),
    ])
    for (const source of sourcePayload.items || []) {
      sources.value = { ...sources.value, [source.id]: source }
    }
    items.value = subscriptions.items || []
    jobs.value = downloads.items || []
    jobStatsData.value = subscriptions.jobStats || {}
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
  openMenu.value = openMenu.value === item.id ? 0 : item.id
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
  if (!form.value.cronExpr.trim()) {
    notify('Cron 表达式不能为空', 'warning')
    return
  }
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
    message: `确定删除订阅「${item.title}」吗？下载文件和任务记录不会被自动删除。`,
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
  if (item.localCover) return api.localFileUrl(item.localCover)
  if (item.cachedCover) return item.cachedCover
  return api.imageUrl(item.cover, item.sourceId)
}

function sourceIcon(item) {
  return sources.value[item.sourceId]?.icon || ''
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
  const aggregated = jobStatsData.value[key]
  if (aggregated) {
    return {
      success: Number(aggregated.succeeded || 0),
      failed: Number(aggregated.failed || 0),
    }
  }
  const related = jobs.value.filter((job) => job.sourceId === item.sourceId && job.comicId === item.comicId)
  return {
    success: related.filter((job) => job.status === 'completed').length,
    failed: related.filter((job) => job.status === 'failed').length,
  }
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
  busy.value = item.id
  try {
    const result = await api.downloadAllSubscription(item.id)
    const queued = Number(result?.queued || 0)
    const skipped = Number(result?.skipped || 0)
    notify(
      [`《${item.title}》全量下载已加入 ${queued} 个章节`, skipped ? `跳过 ${skipped} 个已完成章节` : '']
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
    clearSubscriptionSelection()
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
    message: `确定对所选 ${selectedCount.value} 个订阅执行全量下载吗？已完成的章节会自动跳过。`,
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
    message: `确定删除所选 ${selectedCount.value} 个订阅吗？下载文件和任务记录不会被自动删除。`,
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

  <div v-if="downloadDirs.length" class="local-root-strip" aria-label="订阅目录">
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
      v-for="(item, index) in filteredItems"
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
          <img v-if="cover(item)" :src="cover(item)" :alt="item.title" loading="lazy" />
          <BookOpen v-else :size="24" />
        </ContentLink>
        <div class="subscription-meta">
          <ContentLink
            class="subscription-title"
            :to="{ name: 'comic', params: { sourceId: item.sourceId, comicId: item.comicId } }"
            :title="item.title"
          >
            {{ cardTitle(item) }}
          </ContentLink>
          <span class="subscription-sub">{{ item.author || '未知作者' }}</span>
          <span class="subscription-sub">
            {{ chapterCount(item) }} 话 · 最新章节：{{ item.lastChapterTitle || '尚未记录' }}
          </span>
          <div class="subscription-line">
            <CalendarClock :size="14" />
            <span class="mono">{{ item.cronExpr || '未设置' }}</span>
          </div>
          <div class="subscription-line">
            <FolderOpen :size="14" />
            <span>{{ item.downloadDir || '未设置下载目录' }}</span>
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
          <div v-if="openMenu === item.id" class="dropdown-panel subscription-actions" @click.stop>
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
        <div class="subscription-toggle-grid">
          <label class="switch-row">
            <input v-model="form.chapterMarker" type="checkbox" />
            <span>章节标符</span>
          </label>
          <label class="switch-row">
            <input v-model="form.coverBlur" type="checkbox" />
            <span>封面模糊</span>
          </label>
          <label class="switch-row">
            <input v-model="form.convertToSimplified" type="checkbox" />
            <span>繁转简</span>
          </label>
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
