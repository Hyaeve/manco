<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { BookOpenCheck, ExternalLink, Eye, EyeOff, KeyRound, Loader2, RefreshCw, Save, Server, Trash2 } from 'lucide-vue-next'
import { api } from '../api'
import PasswordInput from '../components/PasswordInput.vue'

const sources = ref([])
const accounts = ref({})
const repoUrl = ref('')
const loading = ref(true)
const busy = ref('')
const error = ref('')
const message = ref('')

const forms = reactive({})
const activeKind = ref('comic')

const comicSources = computed(() => sources.value.filter((item) => (item.kind || 'comic') === 'comic'))
const bookSources = computed(() => sources.value.filter((item) => item.kind === 'book'))
const visibleSources = computed(() => (activeKind.value === 'book' ? bookSources.value : comicSources.value))

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
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
    error.value = err.message
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

async function save(item, login) {
  const form = forms[item.id]
  busy.value = item.id
  error.value = ''
  message.value = ''
  try {
    await api.saveAccount(item.id, {
      username: form.username,
      password: form.password,
      cookie: form.cookie,
      homeUrl: form.homeUrl,
      login: Boolean(login),
    })
    message.value = `${item.name} 凭据已保存。`
    form.password = ''
    form.cookie = ''
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = ''
  }
}

async function toggleHidden(item) {
	busy.value = item.id
	error.value = ''
	message.value = ''
	try {
		const result = await api.updateSource(item.id, { hidden: !item.hidden })
		item.hidden = Boolean(result.hidden)
		message.value = item.hidden ? `${item.name} 已从发现页隐藏。` : `${item.name} 已显示在发现页。`
	} catch (err) {
		error.value = err.message
	} finally {
		busy.value = ''
	}
}

async function disconnect(item) {
  if (!window.confirm(`确定清除「${item.name}」的账号凭据吗？`)) return
  busy.value = item.id
  error.value = ''
  message.value = ''
  try {
    await api.deleteAccount(item.id)
    const form = forms[item.id]
    form.username = ''
    form.password = ''
    form.cookie = ''
    message.value = `${item.name} 的凭据已清除。`
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = ''
  }
}
</script>

<template>
  <div v-if="error" class="alert error">{{ error }}</div>
  <div v-if="message" class="alert ok">{{ message }}</div>

  <div class="card card-pad" style="margin-bottom: 18px">
    <div class="inline">
      <span class="stat-icon"><BookOpenCheck :size="18" /></span>
      <div style="flex: 1; min-width: 220px">
        <strong>Kototoro 拓展仓库</strong>
        <p class="muted small" style="margin: 2px 0 0">
          Manco 直接以 Go 重新实现这些漫画源的访问协议，仓库地址仅作为源清单与更新参考。
        </p>
      </div>
      <a v-if="repoUrl" class="btn secondary small" :href="repoUrl" target="_blank" rel="noreferrer">
        <ExternalLink :size="14" />
        打开仓库
      </a>
      <button class="btn secondary small" type="button" :disabled="loading" @click="load">
        <RefreshCw :size="14" :class="{ spin: loading }" />
        刷新
      </button>
    </div>
    <p v-if="repoUrl" class="muted small" style="margin: 10px 0 0; word-break: break-all">{{ repoUrl }}</p>
  </div>

  <div class="segmented" role="tablist" aria-label="资源类型" style="margin-bottom: 14px">
    <button type="button" :class="{ active: activeKind === 'comic' }" @click="activeKind = 'comic'">
      漫画源
      <span class="segmented-count">{{ comicSources.length }}</span>
    </button>
    <button type="button" :class="{ active: activeKind === 'book' }" @click="activeKind = 'book'">
      书籍源
      <span class="segmented-count">{{ bookSources.length }}</span>
    </button>
  </div>

  <div v-if="loading && !sources.length" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载资源</span>
  </div>
  <div v-else class="source-grid">
    <section v-for="item in visibleSources" :key="item.id" class="card card-pad source-card" :class="{ 'source-card-hidden': item.hidden }">
      <div class="section-head" style="margin-bottom: 8px">
        <div class="inline">
          <img v-if="item.icon" class="source-favicon" :src="item.icon" alt="" />
          <Server v-else :size="17" />
          <h2>{{ item.name }}</h2>
        </div>
        <div class="inline">
          <span v-if="connected(item.id)" class="badge success">已连接</span>
          <span v-else class="badge">未连接</span>
          <button class="btn secondary small" type="button" :disabled="busy === item.id" @click="toggleHidden(item)">
            <Loader2 v-if="busy === item.id" :size="14" class="spin" />
            <EyeOff v-else-if="!item.hidden" :size="14" />
            <Eye v-else :size="14" />
            {{ item.hidden ? '在发现页显示' : '从发现页隐藏' }}
          </button>
        </div>
      </div>
      <p class="muted small" style="margin-top: 0">{{ item.description }}</p>
      <div class="tag-list">
        <span v-if="item.needsLogin" class="badge warning">需要登录</span>
        <span class="badge" :class="item.canSearch ? 'primary' : ''">{{ item.canSearch ? '支持搜索' : '不支持搜索' }}</span>
        <span class="badge" :class="item.canBrowse ? 'primary' : ''">{{ item.canBrowse ? '支持浏览' : '不支持浏览' }}</span>
      </div>

      <template v-if="forms[item.id]">
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
        <template v-else>
          <label class="field" style="margin-bottom: 10px">
            <span>网站地址（可选内置镜像，也可自定义）</span>
            <input
              v-model="forms[item.id].homeUrl"
              class="input"
              :placeholder="item.homepage"
              :list="`sites-${item.id}`"
            />
            <datalist :id="`sites-${item.id}`">
              <option v-for="site in item.sites || []" :key="site" :value="site">{{ site }}</option>
            </datalist>
          </label>
          <label class="field" style="margin-bottom: 12px">
            <span>Cookie（可选，用于通过校验）</span>
            <textarea v-model="forms[item.id].cookie" class="textarea" placeholder="粘贴浏览器中的 Cookie" />
          </label>
        </template>

        <div class="inline">
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
      </template>
    </section>
  </div>
</template>
