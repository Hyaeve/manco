<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import {
  Boxes,
  Check,
  ChevronDown,
  Eye,
  EyeOff,
  KeyRound,
  Loader2,
  PackagePlus,
  Plus,
  RefreshCw,
  Save,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  Trash2,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import { notify } from '../stores/notices'
import PasswordInput from '../components/PasswordInput.vue'
import SitePicker from '../components/SitePicker.vue'

const sources = ref([])
const repositories = ref([])
const accounts = ref({})
const loading = ref(true)
const busy = ref('')
const activeKind = ref('comic')
const repositoryOpen = ref(false)
const categoryOpen = ref(false)
const categorySource = ref(null)
const categorySelection = ref([])
const expandedRepositories = ref({})
const forms = reactive({})
const repositoryForm = reactive({ name: '', url: '', kind: 'json' })

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
const visibleSources = computed(() => (activeKind.value === 'book' ? bookSources.value : comicSources.value))

onMounted(load)

async function load() {
  loading.value = true
  try {
    const [sourcePayload, repositoryPayload] = await Promise.all([api.sources(), api.repositories()])
    sources.value = sourcePayload.items || []
    accounts.value = sourcePayload.accounts || {}
    repositories.value = repositoryPayload.items || []
    for (const item of sources.value) {
      const account = accounts.value[item.id] || {}
      if (!forms[item.id]) {
        forms[item.id] = reactive({
          username: account.username || '',
          password: account.password || '',
          cookie: '',
          homeUrl: account.homeUrl || '',
        })
      } else {
        forms[item.id].username = account.username || ''
        if (account.password) forms[item.id].password = account.password
        forms[item.id].homeUrl = account.homeUrl || ''
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

function connected(id) {
  const account = accountOf(id)
  return Boolean(account && (account.hasToken || account.hasCookie))
}

function isBuiltin(item) {
  return item.builtin || ['picacg', 'jmcomic', 'baozimh', 'biquge'].includes(item.id)
}

function categoryFilters(item) {
  return (item.filters || []).filter((group) => group.key !== 'sort' && group.key !== 'ranking')
}

function openCategory(item) {
  const groups = categoryFilters(item)
  if (!groups.length) {
    notify(`${item.name} 暂无可配置分类`)
    return
  }
  categorySource.value = item
  categorySelection.value = groups
    .flatMap((group) => group.options || [])
    .filter((option) => option.value && !option.disabled)
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
    const allValues = categoryFilters(item)
      .flatMap((group) => group.options || [])
      .map((option) => String(option.value))
      .filter(Boolean)
    const selected = new Set(categorySelection.value)
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

function openRepository() {
  Object.assign(repositoryForm, { name: '', url: '', kind: 'json' })
  repositoryOpen.value = true
}

async function createRepository() {
  if (!repositoryForm.name.trim() || !repositoryForm.url.trim()) {
    notify('仓库名称和地址不能为空', 'warning')
    return
  }
  busy.value = 'repository-create'
  try {
    const created = await api.createRepository({
      name: repositoryForm.name.trim(),
      url: repositoryForm.url.trim(),
      kind: repositoryForm.kind,
    })
    repositories.value = [...repositories.value.filter((item) => item.id !== created.id), created]
    repositoryOpen.value = false
    notify(`${created.name} 已添加，正在同步来源清单`, 'success')
    await syncRepository(created)
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
  if (!window.confirm(`确定删除拓展仓库「${item.name}」吗？`)) return
  busy.value = `repository-${item.id}`
  try {
    await api.deleteRepository(item.id)
    repositories.value = repositories.value.filter((row) => row.id !== item.id)
    notify(`${item.name} 已删除`, 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = ''
  }
}

async function importExtension(repository, extension) {
  if (!extension.installable) {
    notify('该来源为 Android/JVM 插件，当前版本仅登记清单，不能直接执行', 'warning')
    return
  }
  busy.value = `extension-${extension.id}`
  try {
    const result = await api.importRepositoryExtension(repository.id, extension.id)
    const source = result.source
    if (source && !sources.value.some((item) => item.id === source.id)) {
      sources.value = [...sources.value, source]
    }
    notify(`${extension.name} 已加入资源来源`, 'success')
  } catch (err) {
    notify(err.message, 'error')
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
    notify(`${item.name} 凭据已保存`, 'success')
    form.cookie = ''
    await load()
  } catch (err) {
    notify(`${item.name}：${err.message}`, 'error')
  } finally {
    busy.value = ''
  }
}

async function toggleHidden(item) {
  busy.value = item.id
  try {
    const result = await api.updateSource(item.id, { hidden: !item.hidden })
    item.hidden = Boolean(result.hidden)
    notify(item.hidden ? `${item.name} 已从探索发现隐藏` : `${item.name} 已显示在探索发现`, 'success')
  } catch (err) {
    notify(err.message, 'error')
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
    notify(`${item.name} 的凭据已清除`, 'success')
    await load()
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = ''
  }
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
      <button type="button" :class="{ active: activeKind === 'repository' }" @click="activeKind = 'repository'">
        拓展仓库
        <span class="segmented-count">{{ repositories.length }}</span>
      </button>
    </div>
    <span class="spacer" />
    <button v-if="activeKind === 'repository'" class="btn small" type="button" @click="openRepository">
      <Plus :size="15" />
      添加仓库
    </button>
    <button v-else class="btn secondary small" type="button" :disabled="loading" @click="load">
      <RefreshCw :size="15" :class="{ spin: loading }" />
      刷新
    </button>
  </div>

  <div v-if="loading && !sources.length" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载资源仓库</span>
  </div>

  <template v-else-if="activeKind !== 'repository'">
    <div v-if="!visibleSources.length" class="card empty">
      <Server :size="24" />
      <p>暂无{{ activeKind === 'book' ? '书籍' : '漫画' }}来源</p>
    </div>
    <div v-else class="source-grid">
      <section
        v-for="item in visibleSources"
        :key="item.id"
        class="card card-pad source-card"
        :class="{ 'source-card-hidden': item.hidden }"
      >
        <div class="section-head source-card-head">
          <div class="source-title-line">
            <img v-if="item.icon" class="source-favicon" :src="item.icon" alt="" />
            <Server v-else :size="18" />
            <h2>{{ item.name }}</h2>
            <span v-if="isBuiltin(item)" class="badge">默认</span>
            <span v-else class="badge primary">拓展</span>
          </div>
          <div class="source-card-actions">
            <span v-if="connected(item.id)" class="badge success">已连接</span>
            <span v-else-if="item.needsLogin" class="badge">未连接</span>
            <button
              v-if="categoryFilters(item).length"
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
          </div>
        </div>
        <p class="muted small source-card-summary">{{ item.description || item.homepage }}</p>
        <div class="tag-list">
          <span v-if="item.needsLogin" class="badge warning">需要登录</span>
          <span class="badge" :class="item.canSearch ? 'primary' : ''">{{ item.canSearch ? '支持搜索' : '不支持搜索' }}</span>
          <span class="badge" :class="item.canBrowse ? 'primary' : ''">{{ item.canBrowse ? '支持浏览' : '不支持浏览' }}</span>
          <span v-if="item.hidden" class="badge">已隐藏</span>
        </div>

        <div v-if="forms[item.id] && (item.needsLogin || item.id === 'jmcomic' || item.id === 'baozimh')" class="source-account-block">
          <template v-if="item.id === 'picacg'">
            <label class="field">
              <span>账号（邮箱或用户名）</span>
              <input v-model="forms[item.id].username" class="input" autocomplete="username" placeholder="pica@example.com" />
            </label>
            <label class="field">
              <span>密码</span>
              <PasswordInput v-model="forms[item.id].password" autocomplete="current-password" />
            </label>
          </template>
          <template v-else>
            <label class="field">
              <span>网站地址（内置备用地址可选，也可自定义）</span>
              <SitePicker v-model="forms[item.id].homeUrl" :options="item.sites || []" :placeholder="item.homepage" />
            </label>
            <label class="field">
              <span>Cookie（可选，用于通过站点校验）</span>
              <textarea v-model="forms[item.id].cookie" class="textarea" placeholder="粘贴浏览器中的 Cookie" />
            </label>
          </template>
          <div class="source-account-actions">
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
            <button v-if="accountOf(item.id)" class="btn danger small" type="button" :disabled="busy === item.id" @click="disconnect(item)">
              <Trash2 :size="14" />
              清除
            </button>
          </div>
        </div>
      </section>
    </div>
  </template>

  <template v-else>
    <div v-if="!repositories.length" class="card empty repository-empty">
      <Boxes :size="28" />
      <p>还没有拓展仓库。添加 Kototoro、Mihon 或其他兼容仓库后即可在这里同步来源。</p>
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
            <span v-if="extension.pluginType" class="badge">{{ extension.pluginType }}</span>
            <button
              class="btn small"
              type="button"
              :class="{ secondary: !extension.installable }"
              :disabled="busy === `extension-${extension.id}`"
              @click="importExtension(repository, extension)"
            >
              <Loader2 v-if="busy === `extension-${extension.id}`" :size="14" class="spin" />
              <Check v-else-if="extension.installable" :size="14" />
              <ShieldCheck v-else :size="14" />
              {{ extension.installable ? '添加来源' : '仅登记' }}
            </button>
          </article>
        </div>
      </article>
    </div>
  </template>

  <div v-if="categoryOpen" class="modal-backdrop" @click.self="categoryOpen = false">
    <div class="modal">
      <div class="modal-head">
        <div>
          <h2>分类显示设置</h2>
          <p class="muted small">{{ categorySource?.name }} · 默认全部加入探索发现，取消勾选后排除</p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="categoryOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <section v-for="group in categoryFilters(categorySource || {})" :key="group.key" class="settings-block">
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

  <div v-if="repositoryOpen" class="modal-backdrop" @click.self="repositoryOpen = false">
    <div class="modal">
      <div class="modal-head">
        <div>
          <h2>添加拓展仓库</h2>
          <p class="muted small">填入服务器可访问的仓库清单地址，添加后会自动同步来源。</p>
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
          添加并同步
        </button>
      </div>
    </div>
  </div>
</template>
