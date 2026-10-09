<script setup>
import { onMounted, reactive, ref } from 'vue'
import { BookOpenCheck, ExternalLink, KeyRound, Loader2, RefreshCw, Save, Server, Trash2 } from 'lucide-vue-next'
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

  <div v-if="loading && !sources.length" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载漫画源</span>
  </div>
  <div v-else class="source-grid">
    <section v-for="item in sources" :key="item.id" class="card card-pad">
      <div class="section-head" style="margin-bottom: 8px">
        <div class="inline">
          <Server :size="17" />
          <h2>{{ item.name }}</h2>
        </div>
        <span v-if="connected(item.id)" class="badge success">已连接</span>
        <span v-else class="badge">未连接</span>
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
            <span>站点域名（可选）</span>
            <input v-model="forms[item.id].homeUrl" class="input" :placeholder="item.homepage" />
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
