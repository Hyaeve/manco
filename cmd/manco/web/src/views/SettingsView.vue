<script setup>
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import {
  Archive,
  CheckCircle2,
  Download,
  ExternalLink,
  Github,
  Languages,
  Loader2,
  Network,
  RefreshCw,
  Send,
  ShieldCheck,
} from 'lucide-vue-next'
import { api } from '../api'
import { useRoute } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import PasswordInput from '../components/PasswordInput.vue'
import { askConfirm } from '../stores/confirm'
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
  convertToSimplified: true,
  telegramEnabled: false,
  telegramToken: '',
  telegramChatIds: '',
  maxChapterConcurrency: 10,
  maxPageConcurrency: 2,
  batchSize: 30,
  batchIntervalMinutes: 5,
  logMaxEntries: 10000,
  logRetentionDays: 30,
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
    form.convertToSimplified = Boolean(payload.convertToSimplified)
    form.telegramEnabled = Boolean(payload.telegramEnabled)
    form.telegramToken = payload.telegramToken || ''
    form.telegramChatIds = Array.isArray(payload.telegramChatIds) ? payload.telegramChatIds.join(', ') : ''
    form.maxChapterConcurrency = Number(payload.maxChapterConcurrency) || 10
    form.maxPageConcurrency = Number(payload.maxPageConcurrency) || 2
    form.batchSize = Number(payload.batchSize) || 0
    form.batchIntervalMinutes = Number(payload.batchIntervalMinutes) || 0
    form.logMaxEntries = Number(payload.logMaxEntries) || 10000
    form.logRetentionDays = Number(payload.logRetentionDays ?? 30)
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
  const confirmed = await askConfirm({
    title: '保存账号与安全设置',
    message: '确认保存当前账号与安全设置吗？',
    confirmText: '保存',
  })
  if (!confirmed) return
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

async function saveConvertToSimplified() {
  saving.value = 'convert'
  try {
    await api.saveSettings({ convertToSimplified: form.convertToSimplified })
    notify(form.convertToSimplified ? '全局繁转简已开启' : '全局繁转简已关闭', 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    saving.value = ''
  }
}

// 全局下载设置：章节并发是整台实例的上限，不受内容源数量影响。
async function saveLogs() {
  saving.value = 'logs'
  try {
    await api.saveSettings({
      logMaxEntries: Number(form.logMaxEntries) || 10000,
      logRetentionDays: Number(form.logRetentionDays) || 0,
    })
    notify('运行日志设置已保存', 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    saving.value = ''
  }
}

async function saveDownload() {
  saving.value = 'download'
  try {
    await api.saveSettings({
      maxChapterConcurrency: Number(form.maxChapterConcurrency) || 1,
      maxPageConcurrency: Number(form.maxPageConcurrency) || 1,
      batchSize: Number(form.batchSize) || 0,
      batchIntervalMinutes: Number(form.batchIntervalMinutes) || 0,
    })
    notify('下载设置已保存', 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    saving.value = ''
  }
}

async function saveTelegram() {
  saving.value = 'telegram'
  try {
    const chats = form.telegramChatIds
      .split(/[,\s，、]+/)
      .map((value) => value.trim())
      .filter(Boolean)
    await api.saveSettings({
      telegramEnabled: Boolean(form.telegramEnabled),
      telegramToken: form.telegramToken.trim(),
      telegramChatIds: chats,
    })
    notify(form.telegramEnabled ? 'TG 通知已开启' : 'TG 通知已关闭', 'success')
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

  <div v-else class="settings-workspace">
    <header class="settings-page-head">
      <div>
        <span class="settings-eyebrow">Manco</span>
        <h1>系统设置</h1>
        <p>连接、下载、通知与维护选项按用途分组。</p>
      </div>
      <span class="settings-version-pill">{{ currentVersion || '版本未知' }}</span>
    </header>

    <section class="settings-group">
      <header class="settings-group-head">
        <h2>连接与安全</h2>
        <p>账号会话与外部网络入口。</p>
      </header>
      <div class="settings-grid-two">
        <article id="settings-account" class="card card-pad settings-panel">
          <div class="settings-card-title">
            <div class="inline">
              <ShieldCheck :size="18" />
              <h3>账号与安全</h3>
            </div>
            <button class="btn settings-save-button" type="button" :disabled="saving === 'account'" @click="saveAccount">
              <Loader2 v-if="saving === 'account'" :size="15" class="spin" />
              保存
            </button>
          </div>
          <div class="settings-form-grid" @keydown.enter.prevent="saveAccount">
            <label class="field settings-field-span-2">
              <span>账号</span>
              <input v-model="form.username" class="input" autocomplete="username" />
            </label>
            <label class="field settings-field-span-2">
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
        </article>

        <article class="card card-pad settings-panel">
          <div class="settings-card-title">
            <div class="inline">
              <Network :size="18" />
              <h3>系统代理</h3>
            </div>
            <button class="btn settings-save-button" type="button" :disabled="saving === 'proxy'" @click="saveProxy">
              <Loader2 v-if="saving === 'proxy'" :size="15" class="spin" />
              保存
            </button>
          </div>
          <div class="settings-form-stack" @keydown.enter.prevent="saveProxy">
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
        </article>
      </div>
    </section>

    <section class="settings-group">
      <header class="settings-group-head">
        <h2>下载与通知</h2>
        <p>全局并发、暂停策略和 Telegram 通知。</p>
      </header>
      <div class="settings-grid-two settings-grid-balanced">
        <article id="settings-download" class="card card-pad settings-panel">
          <div class="settings-card-title">
            <div class="inline">
              <Download :size="18" />
              <h3>下载设置</h3>
            </div>
            <button class="btn settings-save-button" type="button" :disabled="saving === 'download'" @click="saveDownload">
              <Loader2 v-if="saving === 'download'" :size="15" class="spin" />
              保存
            </button>
          </div>
          <p class="muted small settings-card-help">
            全局章节并发是实例总上限，单内容源仍可在资源拓展中设置更低值。
          </p>
          <div class="settings-form-grid" @keydown.enter.prevent="saveDownload">
            <label class="field">
              <span>全局同时下载章节数</span>
              <input v-model.number="form.maxChapterConcurrency" class="input" type="number" min="1" max="32" />
            </label>
            <label class="field">
              <span>每章图片并发</span>
              <input v-model.number="form.maxPageConcurrency" class="input" type="number" min="1" max="32" />
            </label>
            <div class="settings-pause-row settings-field-span-2">
              <span>每下载</span>
              <input v-model.number="form.batchSize" class="input archive-number" type="number" min="0" max="1000" />
              <span>章后暂停</span>
              <input v-model.number="form.batchIntervalMinutes" class="input archive-number" type="number" min="0" max="1440" />
              <span>分钟</span>
            </div>
            <p class="muted small settings-field-span-2">填 0 表示不启用自动暂停。</p>
          </div>
        </article>

        <article id="settings-telegram" class="card card-pad settings-panel">
          <div class="settings-card-title">
            <div class="inline">
              <Send :size="18" />
              <h3>TG 通知</h3>
            </div>
            <button class="btn settings-save-button" type="button" :disabled="saving === 'telegram'" @click="saveTelegram">
              <Loader2 v-if="saving === 'telegram'" :size="15" class="spin" />
              保存
            </button>
          </div>
          <p class="muted small settings-card-help">
            网络沿用上方系统代理；配置 Token 和允许的 Chat ID 后可接收通知与使用交互指令。
          </p>
          <div class="settings-form-grid" @keydown.enter.prevent="saveTelegram">
            <label class="field settings-field-span-2">
              <span>Bot Token</span>
              <input v-model="form.telegramToken" class="input" autocomplete="off" placeholder="123456:ABC-DEF..." />
            </label>
            <label class="field settings-field-span-2">
              <span>允许的 Chat ID</span>
              <input v-model="form.telegramChatIds" class="input" autocomplete="off" placeholder="多个用逗号分隔，例如 -1001234567890, 123456789" />
            </label>
            <button
              class="function-toggle settings-field-span-2"
              :class="{ active: form.telegramEnabled }"
              type="button"
              @click="form.telegramEnabled = !form.telegramEnabled"
            >
              <Send :size="16" />
              <span>启用 TG 通知</span>
            </button>
          </div>
        </article>
      </div>
    </section>

    <section class="settings-group">
      <header class="settings-group-head">
        <h2>维护与版本</h2>
        <p>归档、日志、语言转换和程序信息。</p>
      </header>
      <div class="settings-grid-three">
        <article id="settings-logs" class="card card-pad settings-panel">
          <div class="settings-card-title">
            <div class="inline">
              <RefreshCw :size="18" />
              <h3>运行日志</h3>
            </div>
            <button class="btn settings-save-button" type="button" :disabled="saving === 'logs'" @click="saveLogs">
              <Loader2 v-if="saving === 'logs'" :size="15" class="spin" />
              保存
            </button>
          </div>
          <p class="muted small settings-card-help">内存条数与落盘保存天数同时生效。</p>
          <div class="settings-form-stack" @keydown.enter.prevent="saveLogs">
            <label class="field">
              <span>保留日志条数</span>
              <input v-model.number="form.logMaxEntries" class="input" type="number" min="100" max="200000" step="100" />
            </label>
            <label class="field">
              <span>日志保存天数</span>
              <input v-model.number="form.logRetentionDays" class="input" type="number" min="0" max="3650" />
            </label>
          </div>
        </article>

        <article id="settings-archive" class="card card-pad settings-panel">
          <div class="settings-card-title">
            <div class="inline">
              <Archive :size="18" />
              <h3>归档与繁转简</h3>
            </div>
            <span class="settings-status-pill">
              <Loader2 v-if="saving === 'archive' || saving === 'convert'" :size="13" class="spin" />
              自动保存
            </span>
          </div>
          <p class="muted small settings-card-help">超期未更新后关闭订阅，关闭满 15 天后自动归档。</p>
          <div class="archive-number-row settings-pause-row">
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
            <span>天未更新</span>
          </div>
          <div id="settings-convert" class="settings-feature-group">
            <div class="inline settings-feature-heading">
              <Languages :size="17" />
              <h3>繁转简</h3>
            </div>
            <label class="switch-row">
              <input v-model="form.convertToSimplified" type="checkbox" @change="saveConvertToSimplified" />
              <span>全局繁转简</span>
            </label>
          </div>
        </article>

        <article id="settings-about" class="card card-pad settings-panel settings-about-card">
          <svg class="settings-about-illustration" viewBox="0 0 240 170" aria-hidden="true">
            <g class="about-book-stack">
              <rect x="118" y="120" width="94" height="18" rx="6" />
              <path d="M128 121h84M128 137h84" />
              <rect x="106" y="102" width="108" height="18" rx="6" />
              <path d="M117 103h97M117 119h97" />
              <rect x="126" y="84" width="88" height="18" rx="6" />
              <path d="M137 85h77M137 101h77" />
              <path class="about-bookmark" d="M198 84v-18l8 5 8-5v18" />
            </g>
            <g class="about-cat">
              <path class="about-cat-ear" d="M46 48 42 24l21 13M99 48l8-24-22 13" />
              <ellipse class="about-cat-body" cx="73" cy="105" rx="35" ry="46" />
              <circle class="about-cat-head" cx="74" cy="54" r="31" />
              <path class="about-cat-patch" d="M48 54c5-17 18-26 34-25 7 11 9 20 4 30-13 7-27 6-38-5Z" />
              <path class="about-cat-eye" d="M59 53h9M82 53h9" />
              <path class="about-cat-nose" d="m74 60 4 4-8 0Z" />
              <path class="about-cat-mouth" d="M70 68c3 3 6 3 9 0M56 64l-17-4M58 70l-16 5M91 64l17-4M89 70l16 5" />
              <path class="about-cat-tail" d="M101 132c26 2 34-18 20-31-9-9-1-20 9-13" />
              <path class="about-cat-paw" d="M58 142h18M82 142h18" />
            </g>
            <g class="about-sparkles">
              <path d="m27 84 3 8 8 3-8 3-3 8-3-8-8-3 8-3Z" />
              <path d="m207 35 2 6 6 2-6 2-2 6-2-6-6-2 6-2Z" />
              <circle cx="178" cy="58" r="3" />
            </g>
          </svg>
          <div class="settings-card-title">
            <div class="inline">
              <CheckCircle2 :size="18" />
              <h3>关于 Manco</h3>
            </div>
          </div>
          <p class="muted small settings-card-help">
            漫画、书籍和视频源的订阅下载管理工具。
          </p>
          <div class="settings-about-meta">
            <span>当前版本</span>
            <strong>{{ currentVersion || '未知' }}</strong>
          </div>
          <div class="settings-about-actions">
            <button class="btn update-button" type="button" :disabled="checkingUpdate" @click="checkUpdate">
              <Loader2 v-if="checkingUpdate" :size="15" class="spin" />
              <RefreshCw v-else :size="15" />
              检查更新
            </button>
            <a class="btn github-button" href="https://github.com/Hyaeve/manco" target="_blank" rel="noreferrer">
              <Github :size="15" />
              GitHub
              <ExternalLink :size="13" />
            </a>
          </div>
        </article>
      </div>
    </section>
  </div>
</template>
