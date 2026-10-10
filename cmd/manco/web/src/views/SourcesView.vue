<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import {
  BookOpenCheck,
  ExternalLink,
  Eye,
  EyeOff,
  KeyRound,
  Loader2,
  Pencil,
  Plus,
  RefreshCw,
  Save,
  Server,
  SlidersHorizontal,
  Trash2,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import { notify } from '../stores/notices'
import PasswordInput from '../components/PasswordInput.vue'
import SitePicker from '../components/SitePicker.vue'

const sources = ref([])
const accounts = ref({})
const repoUrl = ref('')
const repoInfo = ref(null)
const loading = ref(true)
const busy = ref('')
const activeKind = ref('comic')
const editorOpen = ref(false)
const editorMode = ref('create')
const repoBusy = ref(false)
const categoryOpen = ref(false)
const categorySource = ref(null)
const categorySelection = ref([])

const forms = reactive({})
const editor = reactive({
  id: '',
  name: '',
  kind: 'comic',
  description: '',
  homepage: '',
  icon: '',
  repoUrl: '',
  searchUrl: '',
  browseUrl: '',
  itemSelector: '',
  titleSelector: '',
  coverSelector: '',
  linkSelector: '',
  detailTitleSelector: '',
  authorSelector: '',
  descriptionSelector: '',
  chapterSelector: '',
  chapterTitleSelector: '',
  chapterLinkSelector: '',
  pageImageSelector: '',
  contentSelector: '',
  allowedHosts: '',
})

const comicSources = computed(() => sources.value.filter((item) => (item.kind || 'comic') === 'comic'))
const bookSources = computed(() => sources.value.filter((item) => item.kind === 'book'))
const visibleSources = computed(() => (activeKind.value === 'book' ? bookSources.value : comicSources.value))

onMounted(load)

async function load() {
  loading.value = true
  try {
    const payload = await api.sources()
    sources.value = payload.items || []
    accounts.value = payload.accounts || {}
    repoUrl.value = payload.repoUrl || ''
    for (const item of sources.value) {
      const account = accounts.value[item.id] || {}
      if (!forms[item.id]) {
        forms[item.id] = reactive({
          username: account.username || '',
          password: '',
          cookie: '',
          homeUrl: account.homeUrl || '',
        })
      }
    }
  } catch (err) {
    notify(`资源库加载失败：${err.message}`, true)
  } finally {
    loading.value = false
  }
}

async function loadRepo() {
  repoBusy.value = true
  try {
    const payload = await api.sourceRepo()
    const raw = payload.items
    const list = Array.isArray(raw) ? raw : raw && typeof raw === 'object' ? Object.values(raw) : []
    repoInfo.value = list.find((entry) => entry && typeof entry === 'object') || null
    if (repoInfo.value) {
      notify(`已读取 Kototoro 拓展仓库：${repoInfo.value.name || '未命名'}${repoInfo.value.version ? ` · v${repoInfo.value.version}` : ''}`)
    }
  } catch (err) {
    notify(`读取仓库失败：${err.message}`, true)
  } finally {
    repoBusy.value = false
  }
}

function accountOf(id) {
  return accounts.value[id] || null
}

function connected(id) {
  const account = accountOf(id)
  return Boolean(account && (account.hasToken || account.hasCookie))
}

function isBuiltin(item) {
  return item.builtin || ['picacg', 'jmcomic', 'baozimh', 'biquge'].includes(item.id)
}

function openCategory(item) {
  if (!item.filters?.length) {
    notify(`${item.name} 暂无可配置分类`)
    return
  }
  categorySource.value = item
  categorySelection.value = item.filters
    .flatMap((group) => group.options || [])
    .filter((option) => option.disabled && option.value)
    .map((option) => String(option.value))
  categoryOpen.value = true
}

function toggleCategory(value) {
  const key = String(value)
  categorySelection.value = categorySelection.value.includes(key)
    ? categorySelection.value.filter((item) => item !== key)
    : [...categorySelection.value, key]
}

async function saveCategories() {
  const item = categorySource.value
  if (!item) return
  busy.value = item.id
  try {
    const result = await api.updateSource(item.id, { disabledCategories: categorySelection.value })
    const disabled = new Set(result.disabledCategories || [])
    item.filters = (item.filters || []).map((group) => ({
      ...group,
      options: (group.options || []).map((option) => ({ ...option, disabled: disabled.has(String(option.value)) })),
    }))
    categoryOpen.value = false
    notify(`${item.name} 的分类显示设置已保存`)
  } catch (err) {
    notify(err.message, true)
  } finally {
    busy.value = ''
  }
}

function openCreate(kind) {
  editorMode.value = 'create'
  Object.assign(editor, {
    id: '',
    name: '',
    kind,
    description: '',
    homepage: '',
    icon: '',
    repoUrl: repoUrl.value || '',
    searchUrl: '',
    browseUrl: '',
    itemSelector: '',
    titleSelector: '',
    coverSelector: '',
    linkSelector: '',
    detailTitleSelector: '',
    authorSelector: '',
    descriptionSelector: '',
    chapterSelector: '',
    chapterTitleSelector: '',
    chapterLinkSelector: '',
    pageImageSelector: '',
    contentSelector: '',
    allowedHosts: '',
  })
  editorOpen.value = true
  if (!repoInfo.value) loadRepo()
}

async function openEdit(item) {
  if (isBuiltin(item)) return
  editorMode.value = 'edit'
  busy.value = item.id
  try {
    const payload = await api.customSourceConfig(item.id)
    const cfg = payload.config || {}
    Object.assign(editor, {
      id: payload.id || item.id,
      name: payload.name || item.name,
      kind: payload.kind || item.kind || 'comic',
      description: payload.description || item.description || '',
      homepage: payload.homepage || item.homepage || '',
      icon: payload.icon || item.icon || '',
      repoUrl: payload.repoUrl || repoUrl.value || '',
      searchUrl: cfg.searchUrl || '',
      browseUrl: cfg.browseUrl || '',
      itemSelector: cfg.itemSelector || '',
      titleSelector: cfg.titleSelector || '',
      coverSelector: cfg.coverSelector || '',
      linkSelector: cfg.linkSelector || '',
      detailTitleSelector: cfg.detailTitleSelector || '',
      authorSelector: cfg.authorSelector || '',
      descriptionSelector: cfg.descriptionSelector || '',
      chapterSelector: cfg.chapterSelector || '',
      chapterTitleSelector: cfg.chapterTitleSelector || '',
      chapterLinkSelector: cfg.chapterLinkSelector || '',
      pageImageSelector: cfg.pageImageSelector || '',
      contentSelector: cfg.contentSelector || '',
      allowedHosts: (cfg.allowedHosts || []).join(', '),
    })
    editorOpen.value = true
  } catch (err) {
    notify(err.message, true)
  } finally {
    busy.value = ''
  }
}

function buildConfig() {
  const config = {}
  const mapping = {
    searchUrl: editor.searchUrl,
    browseUrl: editor.browseUrl,
    itemSelector: editor.itemSelector,
    titleSelector: editor.titleSelector,
    coverSelector: editor.coverSelector,
    linkSelector: editor.linkSelector,
    detailTitleSelector: editor.detailTitleSelector,
    authorSelector: editor.authorSelector,
    descriptionSelector: editor.descriptionSelector,
    chapterSelector: editor.chapterSelector,
    chapterTitleSelector: editor.chapterTitleSelector,
    chapterLinkSelector: editor.chapterLinkSelector,
    pageImageSelector: editor.pageImageSelector,
    contentSelector: editor.contentSelector,
  }
  for (const [key, value] of Object.entries(mapping)) {
    if (String(value || '').trim()) config[key] = String(value).trim()
  }
  const hosts = String(editor.allowedHosts || '')
    .split(/[\s,]+/)
    .map((item) => item.trim())
    .filter(Boolean)
  if (hosts.length) config.allowedHosts = hosts
  return config
}

async function saveEditor() {
  if (!editor.id.trim() || !editor.name.trim() || !editor.homepage.trim()) {
    notify('源 ID、名称和首页地址不能为空', true)
    return
  }
  const config = buildConfig()
  if (!config.searchUrl && !config.itemSelector) {
    notify('至少填写“搜索地址”或“列表项选择器”其中之一', true)
    return
  }
  busy.value = editor.id
  const payload = {
    id: editor.id.trim().toLowerCase(),
    name: editor.name.trim(),
    kind: editor.kind,
    description: editor.description.trim(),
    homepage: editor.homepage.trim(),
    icon: editor.icon.trim(),
    repoUrl: editor.repoUrl.trim(),
    config,
    enabled: true,
  }
  try {
    if (editorMode.value === 'create') {
      await api.createCustomSource(payload)
    } else {
      await api.updateCustomSource(editor.id.trim().toLowerCase(), payload)
    }
    notify(`${payload.name} 已保存，可在探索发现中浏览。`)
    editorOpen.value = false
    await load()
  } catch (err) {
    notify(err.message, true)
  } finally {
    busy.value = ''
  }
}

async function removeCustom(item) {
  if (!window.confirm(`确定删除自定义源「${item.name}」吗？`)) return
  busy.value = item.id
  try {
    await api.deleteCustomSource(item.id)
    sources.value = sources.value.filter((row) => row.id !== item.id)
    notify(`${item.name} 已删除。`)
  } catch (err) {
    notify(err.message, true)
  } finally {
    busy.value = ''
  }
}

async function save(item, login) {
  const form = forms[item.id]
  busy.value = item.id
  try {
    await api.saveAccount(item.id, {
      username: form.username,
      password: form.password,
      cookie: form.cookie,
      homeUrl: form.homeUrl,
      login: Boolean(login),
    })
    notify(`${item.name} 凭据已保存。`)
    form.password = ''
    form.cookie = ''
    await load()
  } catch (err) {
    notify(`${item.name}：${err.message}`, true)
  } finally {
    busy.value = ''
  }
}

async function toggleHidden(item) {
  busy.value = item.id
  try {
    const result = await api.updateSource(item.id, { hidden: !item.hidden })
    item.hidden = Boolean(result.hidden)
    notify(item.hidden ? `${item.name} 已从探索发现隐藏。` : `${item.name} 已显示在探索发现。`)
  } catch (err) {
    notify(err.message, true)
  } finally {
    busy.value = ''
  }
}

async function disconnect(item) {
  if (!window.confirm(`确定清除「${item.name}」的账号凭据吗？`)) return
  busy.value = item.id
  try {
    await api.deleteAccount(item.id)
    const form = forms[item.id]
    form.username = ''
    form.password = ''
    form.cookie = ''
    notify(`${item.name} 的凭据已清除。`)
    await load()
  } catch (err) {
    notify(err.message, true)
  } finally {
    busy.value = ''
  }
}
</script>

<template>

  <div class="card card-pad" style="margin-bottom: 18px">
    <div class="inline">
      <span class="stat-icon"><BookOpenCheck :size="18" /></span>
      <div style="flex: 1; min-width: 220px">
        <strong>Kototoro 拓展仓库</strong>
        <p class="muted small" style="margin: 2px 0 0">
          以 Kototoro 拓展仓库为源清单参考，用 Go 选择器适配器安全地接入站点；默认源为内置实现，保持不可删改。
        </p>
      </div>
      <a v-if="repoUrl" class="btn secondary small" :href="repoUrl" target="_blank" rel="noreferrer">
        <ExternalLink :size="14" />
        仓库
      </a>
      <button class="btn secondary small" type="button" :disabled="repoBusy" @click="loadRepo">
        <Loader2 v-if="repoBusy" :size="14" class="spin" />
        <BookOpenCheck v-else :size="14" />
        读取仓库
      </button>
      <button class="btn secondary small" type="button" :disabled="loading" @click="load">
        <RefreshCw :size="14" :class="{ spin: loading }" />
        刷新
      </button>
    </div>
    <div v-if="repoInfo" class="repo-meta">
      <span class="badge primary">{{ repoInfo.name || 'Kototoro Parsers' }}</span>
      <span v-if="repoInfo.version" class="badge">v{{ repoInfo.version }}</span>
      <span v-if="repoInfo.pkg" class="muted small mono">{{ repoInfo.pkg }}</span>
    </div>
    <p v-else-if="repoUrl" class="muted small" style="margin: 10px 0 0; word-break: break-all">{{ repoUrl }}</p>
  </div>

  <div class="toolbar">
    <div class="segmented" role="tablist" aria-label="资源类型">
      <button type="button" :class="{ active: activeKind === 'comic' }" @click="activeKind = 'comic'">
        漫画源
        <span class="segmented-count">{{ comicSources.length }}</span>
      </button>
      <button type="button" :class="{ active: activeKind === 'book' }" @click="activeKind = 'book'">
        书籍源
        <span class="segmented-count">{{ bookSources.length }}</span>
      </button>
    </div>
    <span class="spacer" />
    <button class="btn small" type="button" @click="openCreate(activeKind)">
      <Plus :size="15" />
      添加{{ activeKind === 'book' ? '书籍源' : '漫画源' }}
    </button>
  </div>

  <div v-if="loading && !sources.length" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载资源</span>
  </div>
  <div v-else class="source-grid">
    <section
      v-for="item in visibleSources"
      :key="item.id"
      class="card card-pad source-card"
      :class="{ 'source-card-hidden': item.hidden }"
      @click="openCategory(item)"
    >
      <div class="section-head" style="margin-bottom: 8px" @click.stop>
        <div class="inline">
          <img v-if="item.icon" class="source-favicon" :src="item.icon" alt="" />
          <Server v-else :size="17" />
          <h2>{{ item.name }}</h2>
          <span v-if="isBuiltin(item)" class="badge">默认</span>
          <span v-else class="badge primary">自定义</span>
        </div>
        <div class="inline">
          <span v-if="connected(item.id)" class="badge success">已连接</span>
          <span v-else-if="item.needsLogin" class="badge">未连接</span>
          <button
            v-if="item.filters?.length"
            class="btn secondary small"
            type="button"
            :disabled="busy === item.id"
            @click="openCategory(item)"
          >
            <SlidersHorizontal :size="14" />
            分类
          </button>
          <button class="btn secondary small" type="button" :disabled="busy === item.id" @click="toggleHidden(item)">
            <Loader2 v-if="busy === item.id" :size="14" class="spin" />
            <EyeOff v-else-if="!item.hidden" :size="14" />
            <Eye v-else :size="14" />
            {{ item.hidden ? '显示' : '隐藏' }}
          </button>
          <button
            v-if="!isBuiltin(item)"
            class="btn secondary small"
            type="button"
            :disabled="busy === item.id"
            @click="openEdit(item)"
          >
            <Pencil :size="14" />
            编辑
          </button>
          <button
            v-if="!isBuiltin(item)"
            class="btn danger small"
            type="button"
            :disabled="busy === item.id"
            @click="removeCustom(item)"
          >
            <Trash2 :size="14" />
          </button>
        </div>
      </div>
      <p class="muted small source-card-summary" style="margin-top: 0" @click.stop>{{ item.description || item.homepage }}</p>
      <div class="tag-list" @click.stop>
        <span v-if="item.needsLogin" class="badge warning">需要登录</span>
        <span class="badge" :class="item.canSearch ? 'primary' : ''">{{ item.canSearch ? '支持搜索' : '不支持搜索' }}</span>
        <span class="badge" :class="item.canBrowse ? 'primary' : ''">{{ item.canBrowse ? '支持浏览' : '不支持浏览' }}</span>
        <span v-if="item.hidden" class="badge">已隐藏</span>
      </div>
      <a v-if="item.homepage && !isBuiltin(item)" class="muted small" :href="item.homepage" target="_blank" rel="noreferrer" @click.stop>
        {{ item.homepage }}
      </a>

      <div v-if="forms[item.id] && isBuiltin(item)" class="source-account-block" @click.stop>
        <template v-if="item.id === 'picacg'">
          <label class="field" style="margin-bottom: 10px">
            <span>账号（邮箱或用户名）</span>
            <input v-model="forms[item.id].username" class="input" autocomplete="username" placeholder="pica@example.com" />
          </label>
          <label class="field" style="margin-bottom: 12px">
            <span>密码</span>
            <PasswordInput v-model="forms[item.id].password" autocomplete="current-password" />
          </label>
        </template>
        <template v-else-if="item.id === 'jmcomic' || item.id === 'baozimh'">
          <label class="field" style="margin-bottom: 10px">
            <span>网站地址（可选内置镜像，也可自定义）</span>
            <SitePicker
              v-model="forms[item.id].homeUrl"
              :options="item.sites || []"
              :placeholder="item.homepage"
            />
          </label>
          <label class="field" style="margin-bottom: 12px">
            <span>Cookie（可选，用于通过校验）</span>
            <textarea v-model="forms[item.id].cookie" class="textarea" placeholder="粘贴浏览器中的 Cookie" />
          </label>
        </template>
        <p v-else class="muted small">该内置书籍源无需额外凭据。</p>

        <div v-if="item.needsLogin || item.id === 'jmcomic' || item.id === 'baozimh'" class="inline">
          <button
            v-if="item.id === 'picacg'"
            class="btn small"
            type="button"
            :disabled="busy === item.id"
            @click="save(item, true)"
          >
            <Loader2 v-if="busy === item.id" :size="14" class="spin" />
            <KeyRound v-else :size="14" />
            登录并保存
          </button>
          <button v-else class="btn small" type="button" :disabled="busy === item.id" @click="save(item, false)">
            <Loader2 v-if="busy === item.id" :size="14" class="spin" />
            <Save v-else :size="14" />
            保存凭据
          </button>
          <button
            v-if="item.id === 'picacg'"
            class="btn secondary small"
            type="button"
            :disabled="busy === item.id"
            @click="save(item, false)"
          >
            <Save :size="14" />
            仅保存账号
          </button>
          <button
            v-if="accountOf(item.id)"
            class="btn danger small"
            type="button"
            :disabled="busy === item.id"
            @click="disconnect(item)"
          >
            <Trash2 :size="14" />
            清除
          </button>
          <span v-if="accountOf(item.id)?.updatedAt" class="muted small">
            更新于 {{ new Date(accountOf(item.id).updatedAt).toLocaleString() }}
          </span>
        </div>
      </div>
    </section>
  </div>

  <div v-if="categoryOpen" class="modal-backdrop" @click.self="categoryOpen = false">
    <div class="modal">
      <div class="modal-head">
        <div>
          <h2>分类显示设置</h2>
          <p class="muted small">{{ categorySource?.name }} · 勾选后不在探索发现显示</p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="categoryOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <section v-for="group in categorySource?.filters || []" :key="group.key" class="settings-block">
          <h3>{{ group.label }}</h3>
          <div class="category-picker-grid">
            <label v-for="option in group.options || []" :key="`${group.key}-${option.value}`" class="category-toggle">
              <input
                type="checkbox"
                :checked="categorySelection.includes(String(option.value))"
                @change="toggleCategory(option.value)"
              />
              <span>{{ option.label }}</span>
            </label>
          </div>
        </section>
      </div>
      <div class="modal-foot">
        <button class="btn secondary" type="button" @click="categoryOpen = false">取消</button>
        <button class="btn" type="button" :disabled="busy === categorySource?.id" @click="saveCategories">
          <Loader2 v-if="busy === categorySource?.id" :size="15" class="spin" />
          <Save v-else :size="15" />
          保存分类设置
        </button>
      </div>
    </div>
  </div>

  <div v-if="editorOpen" class="modal-backdrop" @click.self="editorOpen = false">
    <div class="modal modal-lg">
      <div class="modal-head">
        <div>
          <h2>{{ editorMode === 'create' ? '添加自定义源' : '编辑自定义源' }}</h2>
          <p class="muted small">使用选择器描述站点结构，保存后即可在探索发现中浏览。</p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="editorOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <div class="settings-grid">
          <label class="field">
            <span>源 ID</span>
            <input v-model="editor.id" class="input" :disabled="editorMode === 'edit'" placeholder="example-comic" />
          </label>
          <label class="field">
            <span>名称</span>
            <input v-model="editor.name" class="input" placeholder="示例漫画" />
          </label>
          <label class="field">
            <span>类型</span>
            <select v-model="editor.kind" class="input">
              <option value="comic">漫画源</option>
              <option value="book">书籍源</option>
            </select>
          </label>
          <label class="field">
            <span>首页地址</span>
            <input v-model="editor.homepage" class="input" placeholder="https://example.com" />
          </label>
          <label class="field">
            <span>图标地址（可选）</span>
            <input v-model="editor.icon" class="input" placeholder="https://example.com/favicon.ico" />
          </label>
          <label class="field">
            <span>拓展仓库地址（可选）</span>
            <input v-model="editor.repoUrl" class="input" placeholder="https://raw.githubusercontent.com/.../index.min.json" />
          </label>
        </div>
        <label class="field">
          <span>简介</span>
          <input v-model="editor.description" class="input" placeholder="来源说明" />
        </label>

        <details class="advanced-box" open>
          <summary>选择器配置</summary>
          <div class="settings-grid">
            <label class="field">
              <span>搜索地址（支持 {query} / {page} 占位）</span>
              <input v-model="editor.searchUrl" class="input" placeholder="https://example.com/search?q={query}&page={page}" />
            </label>
            <label class="field">
              <span>浏览地址（支持 {page} 占位）</span>
              <input v-model="editor.browseUrl" class="input" placeholder="https://example.com/list?page={page}" />
            </label>
            <label class="field">
              <span>列表项选择器</span>
              <input v-model="editor.itemSelector" class="input" placeholder=".comic-item" />
            </label>
            <label class="field">
              <span>标题选择器</span>
              <input v-model="editor.titleSelector" class="input" placeholder=".title" />
            </label>
            <label class="field">
              <span>封面选择器</span>
              <input v-model="editor.coverSelector" class="input" placeholder="img" />
            </label>
            <label class="field">
              <span>作品链接选择器</span>
              <input v-model="editor.linkSelector" class="input" placeholder="a" />
            </label>
            <label class="field">
              <span>详情标题选择器</span>
              <input v-model="editor.detailTitleSelector" class="input" placeholder="h1" />
            </label>
            <label class="field">
              <span>作者选择器</span>
              <input v-model="editor.authorSelector" class="input" placeholder=".author" />
            </label>
            <label class="field">
              <span>简介选择器</span>
              <input v-model="editor.descriptionSelector" class="input" placeholder=".summary" />
            </label>
            <label class="field">
              <span>章节项选择器</span>
              <input v-model="editor.chapterSelector" class="input" placeholder=".chapter-item" />
            </label>
            <label class="field">
              <span>章节标题选择器</span>
              <input v-model="editor.chapterTitleSelector" class="input" placeholder="a" />
            </label>
            <label class="field">
              <span>章节链接选择器</span>
              <input v-model="editor.chapterLinkSelector" class="input" placeholder="a" />
            </label>
            <label class="field">
              <span>阅读页图片选择器</span>
              <input v-model="editor.pageImageSelector" class="input" placeholder=".reader img" />
            </label>
            <label class="field">
              <span>书籍正文选择器</span>
              <input v-model="editor.contentSelector" class="input" placeholder=".content" />
            </label>
            <label class="field">
              <span>允许的图床域名（逗号分隔）</span>
              <input v-model="editor.allowedHosts" class="input" placeholder="cdn.example.com" />
            </label>
          </div>
        </details>
      </div>
      <div class="modal-foot">
        <button class="btn secondary" type="button" @click="editorOpen = false">取消</button>
        <button class="btn" type="button" :disabled="busy === editor.id" @click="saveEditor">
          <Loader2 v-if="busy === editor.id" :size="15" class="spin" />
          <Save v-else :size="15" />
          保存
        </button>
      </div>
    </div>
  </div>
</template>
