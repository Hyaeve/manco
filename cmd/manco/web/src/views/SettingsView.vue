<script setup>
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import {
  Archive,
  CheckCircle2,
  ExternalLink,
  Github,
  Loader2,
  Network,
  RefreshCw,
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
const passwordChanged = ref(false)
let archiveTimer = 0
const form = reactive({
  username: '',
  currentPassword: '',
  sessionTtlDays: 30,
  proxy: '',
  proxyUsername: '',
  proxyPassword: '',
  staleDays: 30,
})

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

onUnmounted(() => {
  if (archiveTimer) window.clearTimeout(archiveTimer)
})

async function load() {
  loading.value = true
  try {
    const [payload, versionPayload] = await Promise.all([
      api.settings(),
      api.version().catch(() => ({ version: '' })),
    ])
    form.username = payload.username || auth.user?.username || ''
    form.currentPassword = ''
    passwordChanged.value = false
    form.sessionTtlDays = Number(payload.sessionTtlDays) || 30
    form.proxy = payload.proxy || ''
    form.proxyUsername = payload.proxyUsername || ''
    form.proxyPassword = payload.proxyPassword || ''
    form.staleDays = Number(payload.staleDays?.['*'] ?? 30)
    currentVersion.value = versionPayload.version || ''
  } catch (err) {
    notify(`设置加载失败：${err.message}`, 'error')
  } finally {
    loading.value = false
  }
}

function passwordInput(value) {
  passwordChanged.value = Boolean(String(value || '').length)
}

async function saveAccount() {
  if (!window.confirm('确认保存账号与安全设置吗？')) return
  saving.value = 'account'
  try {
    const payload = {
      username: form.username,
      sessionTtlDays: Number(form.sessionTtlDays) || 30,
    }
    if (passwordChanged.value) {
      payload.newPassword = form.currentPassword
    }
    await api.saveSettings(payload)
    form.currentPassword = ''
    passwordChanged.value = false
    if (auth.user) auth.user = { ...auth.user, username: form.username }
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

function scheduleArchiveSave() {
  if (archiveTimer) window.clearTimeout(archiveTimer)
  archiveTimer = window.setTimeout(() => {
    archiveTimer = 0
    saveArchive()
  }, 600)
}

async function saveArchive() {
  saving.value = 'archive'
  try {
    const days = Math.max(0, Number(form.staleDays) || 0)
    form.staleDays = days
    await api.saveSettings({ staleDays: { '*': days } })
    notify(days === 0 ? '全局订阅归档已停用' : `全局订阅归档已设置为 ${days} 天`, 'success')
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

  <div v-else class="settings-sections settings-v2">
    <div class="settings-pair">
      <section id="settings-account" class="card card-pad settings-compact-card">
        <div class="settings-card-title">
          <div class="inline">
            <ShieldCheck :size="17" />
            <h2>账号与安全</h2>
          </div>
          <button class="btn settings-save-button" type="button" :disabled="saving === 'account'" @click="saveAccount">
            <Loader2 v-if="saving === 'account'" :size="15" class="spin" />
            保存
          </button>
        </div>
        <div class="settings-form-stack">
          <label class="field">
            <span>账号</span>
            <input v-model="form.username" class="input" autocomplete="username" />
          </label>
          <label class="field">
            <span>密码</span>
            <PasswordInput
              v-model="form.currentPassword"
              autocomplete="new-password"
              placeholder="输入新密码（留空不修改）"
              @update:model-value="passwordInput"
            />
          </label>
          <label class="field">
            <span>会话存活期（天）</span>
            <input v-model.number="form.sessionTtlDays" class="input" type="number" min="1" max="3650" />
          </label>
        </div>
      </section>

      <section class="card card-pad settings-compact-card">
        <div class="settings-card-title">
          <div class="inline">
            <Network :size="17" />
            <h2>代理</h2>
          </div>
          <button class="btn settings-save-button" type="button" :disabled="saving === 'proxy'" @click="saveProxy">
            <Loader2 v-if="saving === 'proxy'" :size="15" class="spin" />
            保存
          </button>
        </div>
        <div class="settings-form-stack">
          <label class="field">
            <span>代理地址</span>
            <input v-model="form.proxy" class="input" placeholder="http://127.0.0.1:7890" />
          </label>
          <label class="field">
            <span>用户名</span>
            <input v-model="form.proxyUsername" class="input" autocomplete="off" />
          </label>
          <label class="field">
            <span>密码</span>
            <PasswordInput v-model="form.proxyPassword" autocomplete="off" placeholder="代理密码" />
          </label>
        </div>
      </section>
    </div>

    <div class="settings-pair settings-bottom-pair">
      <section id="settings-archive" class="card card-pad settings-compact-card">
        <div class="settings-card-title">
          <div class="inline">
            <Archive :size="17" />
            <h2>订阅归档</h2>
          </div>
          <Loader2 v-if="saving === 'archive'" :size="16" class="spin muted" />
        </div>
        <p class="muted small settings-card-help">
          全局设置。超过设定天数没有检测到更新时关闭订阅；关闭满 15 天无人重新启用后自动归档。
        </p>
        <div class="archive-number-row">
          <span>超过</span>
          <input
            v-model.number="form.staleDays"
            class="input archive-number"
            type="number"
            min="0"
            max="3650"
            step="1"
            @input="scheduleArchiveSave"
          />
          <span>天未更新后关闭订阅</span>
        </div>
        <p class="muted small">留空或填 0 表示不启用，修改后自动保存。</p>
      </section>

      <section id="settings-about" class="card card-pad settings-compact-card">
        <div class="settings-card-title">
          <div class="inline">
            <CheckCircle2 :size="17" />
            <h2>关于 Manco</h2>
          </div>
        </div>
        <p class="muted small settings-card-help">
          Manco 是漫画与书籍订阅下载工具，把来源里的章节打包为本地文件并持续跟踪更新。
        </p>
        <div class="inline settings-about-actions">
          <button class="btn update-button" type="button" :disabled="checkingUpdate" @click="checkUpdate">
            <Loader2 v-if="checkingUpdate" :size="15" class="spin" />
            <RefreshCw v-else :size="15" />
            检查更新
          </button>
          <a class="btn github-button" href="https://github.com/Hyaeve/manco" target="_blank" rel="noreferrer">
            <Github :size="15" />
            访问 GitHub
            <ExternalLink :size="13" />
          </a>
        </div>
      </section>
    </div>
  </div>
</template>
