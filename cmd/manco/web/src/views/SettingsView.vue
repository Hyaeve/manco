<script setup>
import { onMounted, reactive, ref } from 'vue'
import {
  Archive,
  CheckCircle2,
  ExternalLink,
  Github,
  KeyRound,
  Loader2,
  Network,
  RefreshCw,
  Save,
  ShieldCheck,
} from 'lucide-vue-next'
import { api } from '../api'
import { useRoute } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import PasswordInput from '../components/PasswordInput.vue'
import { notify } from '../stores/notices'

const auth = useAuthStore()
const route = useRoute()
const loading = ref(true)
const saving = ref('')
const checkingUpdate = ref(false)
const currentVersion = ref('')
const form = reactive({
  username: '',
  currentPassword: '',
  newPassword: '',
  confirmPassword: '',
  sessionTtlDays: 30,
  proxy: '',
  proxyUsername: '',
  proxyPassword: '',
  staleDays: { picacg: 0, jmcomic: 0, baozimh: 0, biquge: 0 },
})

const sourceLabels = {
  picacg: '哔咔漫画',
  jmcomic: '禁漫天堂',
  baozimh: '包子漫画',
  biquge: '笔趣阁',
}

onMounted(async () => {
  await load()
  const section = String(route.query.section || '')
  if (section) {
    window.setTimeout(
      () => document.getElementById(`settings-${section}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' }),
      60,
    )
  }
})

async function load() {
  loading.value = true
  try {
    const [payload, versionPayload] = await Promise.all([
      api.settings(),
      api.version().catch(() => ({ version: '' })),
    ])
    form.username = payload.username || auth.user?.username || ''
    form.sessionTtlDays = payload.sessionTtlDays || 30
    form.proxy = payload.proxy || ''
    form.proxyUsername = payload.proxyUsername || ''
    form.proxyPassword = payload.proxyPassword || ''
    form.staleDays = { picacg: 0, jmcomic: 0, baozimh: 0, biquge: 0, ...(payload.staleDays || {}) }
    currentVersion.value = versionPayload.version || ''
  } catch (err) {
    notify(`设置加载失败：${err.message}`, 'error')
  } finally {
    loading.value = false
  }
}

async function saveAccount() {
  if (form.newPassword && form.newPassword !== form.confirmPassword) {
    notify('两次输入的新密码不一致', 'warning')
    return
  }
  if (!window.confirm('确认保存账号与安全设置吗？修改密码后请使用新密码登录。')) return
  saving.value = 'account'
  try {
    await api.saveSettings({
      username: form.username,
      currentPassword: form.currentPassword,
      newPassword: form.newPassword,
      sessionTtlDays: Number(form.sessionTtlDays) || 30,
    })
    if (form.newPassword) {
      form.currentPassword = ''
      form.newPassword = ''
      form.confirmPassword = ''
      if (auth.user) auth.user = { ...auth.user, username: form.username }
    }
    notify('账号与安全设置已保存', 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    saving.value = ''
  }
}

async function saveProxy() {
  saving.value = 'proxy'
  try {
    await api.saveSettings({
      proxy: form.proxy,
      proxyUsername: form.proxyUsername,
      proxyPassword: form.proxyPassword,
    })
    notify('代理设置已保存', 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    saving.value = ''
  }
}

async function saveArchive() {
  saving.value = 'archive'
  try {
    await api.saveSettings({
      staleDays: {
        picacg: Number(form.staleDays.picacg) || 0,
        jmcomic: Number(form.staleDays.jmcomic) || 0,
        baozimh: Number(form.staleDays.baozimh) || 0,
        biquge: Number(form.staleDays.biquge) || 0,
      },
    })
    notify('订阅归档设置已保存', 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    saving.value = ''
  }
}

async function checkUpdate() {
  checkingUpdate.value = true
  try {
    const result = await api.checkUpdate()
    currentVersion.value = result.current || currentVersion.value
    if (result.hasUpdate) {
      notify(`发现新版本 ${result.latest}，当前 ${result.current}`, 'info', { timeout: 8000 })
    } else {
      notify(`当前已是最新版本 ${result.current}`, 'success')
    }
  } catch (err) {
    notify(`检查更新失败：${err.message}`, 'error')
  } finally {
    checkingUpdate.value = false
  }
}
</script>

<template>
  <div v-if="loading" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载设置</span>
  </div>

  <div v-else class="settings-sections">
    <div class="settings-pair">
    <section id="settings-account" class="card card-pad settings-account-card">
      <div class="section-head">
        <div class="inline">
          <ShieldCheck :size="17" />
          <h2>账号与安全</h2>
        </div>
        <span class="badge primary">{{ currentVersion || 'Manco' }}</span>
      </div>
      <div class="settings-account-row">
        <label class="field">
          <span>账号</span>
          <input v-model="form.username" class="input" autocomplete="username" />
        </label>
        <label class="field">
          <span>会话存活期（天）</span>
          <input v-model.number="form.sessionTtlDays" class="input" type="number" min="1" max="3650" />
        </label>
      </div>
      <div class="settings-account-row">
        <label class="field">
          <span>当前密码</span>
          <PasswordInput v-model="form.currentPassword" autocomplete="current-password" />
        </label>
        <label class="field">
          <span>新密码</span>
          <PasswordInput v-model="form.newPassword" autocomplete="new-password" />
        </label>
        <label class="field">
          <span>确认新密码</span>
          <PasswordInput v-model="form.confirmPassword" autocomplete="new-password" />
        </label>
      </div>
      <div class="inline settings-actions">
        <button class="btn" type="button" :disabled="saving === 'account'" @click="saveAccount">
          <Loader2 v-if="saving === 'account'" :size="15" class="spin" />
          <KeyRound v-else :size="15" />
          保存账号与安全
        </button>
      </div>
    </section>

    <section class="card card-pad">
      <div class="section-head">
        <div class="inline">
          <Network :size="17" />
          <h2>代理</h2>
        </div>
      </div>
      <label class="field">
        <span>HTTP / HTTPS 代理地址</span>
        <input v-model="form.proxy" class="input" placeholder="http://127.0.0.1:7890" />
      </label>
      <div class="settings-grid">
        <label class="field">
          <span>代理用户名（可选）</span>
          <input v-model="form.proxyUsername" class="input" autocomplete="off" />
        </label>
        <label class="field">
          <span>代理密码（可选）</span>
          <PasswordInput v-model="form.proxyPassword" autocomplete="off" />
        </label>
      </div>
      <div class="inline settings-actions">
        <button class="btn" type="button" :disabled="saving === 'proxy'" @click="saveProxy">
          <Loader2 v-if="saving === 'proxy'" :size="15" class="spin" />
          <Save v-else :size="15" />
          保存代理
        </button>
      </div>
    </section>
    </div>

    <section class="card card-pad settings-archive-card">
      <div class="section-head">
        <div class="inline">
          <Archive :size="17" />
          <h2>订阅归档</h2>
        </div>
      </div>
      <p class="muted small settings-card-help">
        留空或填 0 表示不启用。超过设定天数没有检测到更新时关闭订阅，关闭满 15 天且无人重新启用后自动归档。
      </p>
      <div class="settings-grid settings-grid-4">
        <label v-for="(label, id) in sourceLabels" :key="id" class="field">
          <span>{{ label }}</span>
          <input v-model.number="form.staleDays[id]" class="input" type="number" min="0" max="3650" placeholder="0" />
        </label>
      </div>
      <div class="inline settings-actions">
        <button class="btn" type="button" :disabled="saving === 'archive'" @click="saveArchive">
          <Loader2 v-if="saving === 'archive'" :size="15" class="spin" />
          <Save v-else :size="15" />
          保存归档设置
        </button>
      </div>
    </section>

    <section id="settings-about" class="card card-pad settings-about-card">
      <div class="section-head">
        <div class="inline">
          <CheckCircle2 :size="17" />
          <h2>关于 Manco</h2>
        </div>
        <span class="badge">{{ currentVersion || '未知版本' }}</span>
      </div>
      <p class="muted small settings-card-help">
        Manco 是漫画与书籍订阅下载工具，来自资源仓库的作品会按章节打包并保存到本地。
      </p>
      <div class="about-grid">
        <div><span class="muted small">运行端口</span><strong>15600</strong></div>
        <div><span class="muted small">容器镜像</span><strong class="mono">ghcr.io/hyaeve/manco:latest</strong></div>
        <div><span class="muted small">项目仓库</span><strong class="mono">github.com/Hyaeve/manco</strong></div>
      </div>
      <div class="inline settings-actions">
        <button class="btn" type="button" :disabled="checkingUpdate" @click="checkUpdate">
          <Loader2 v-if="checkingUpdate" :size="15" class="spin" />
          <RefreshCw v-else :size="15" />
          检查更新
        </button>
        <a class="btn secondary" href="https://github.com/Hyaeve/manco" target="_blank" rel="noreferrer">
          <Github :size="15" />
          访问 GitHub
          <ExternalLink :size="13" />
        </a>
      </div>
    </section>
  </div>
</template>
