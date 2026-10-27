<script setup>
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import {
  Archive,
  CheckCircle2,
  Download,
  ExternalLink,
  Github,
  Languages,
  KeyRound,
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
  bangumiAccessToken: '',
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
    form.bangumiAccessToken = payload.bangumiAccessToken || ''
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
      bangumiAccessToken: form.bangumiAccessToken.trim(),
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
    notify(
      days === 0
        ? '未完结作品的自动归档已停用，已完结作品仍按 15 天规则归档'
        : `未完结作品将在连续 ${days} 天无新章节后自动归档`,
      'success',
    )
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
            <div class="field settings-field-span-2">
              <div class="bangumi-key-head">
                <span class="inline settings-inline-label"><KeyRound :size="15" /> Bangumi Key</span>
                <a
                  class="bangumi-key-apply"
                  href="https://next.bgm.tv/demo/access-token"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  申请个人 Key
                  <ExternalLink :size="12" />
                </a>
              </div>
              <PasswordInput
                v-model="form.bangumiAccessToken"
                autocomplete="off"
                placeholder="留空使用公共请求"
              />
            </div>
            <p class="muted small settings-field-span-2">填写 Bangumi 个人 Access Token 可降低公共接口限流影响。</p>
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
          <p class="muted small settings-card-help">
            已完结作品连续 15 天无新章节自动归档；其他作品超过下方天数无新章节自动归档，填 0 仅停用未完结作品的自动归档。归档前 3 天会在当天 12:00 发送预警。
          </p>
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
            <span>天未检测到新章节</span>
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
          <svg
            class="settings-about-illustration"
            viewBox="0 0 420 280"
            preserveAspectRatio="xMidYMid slice"
            aria-hidden="true"
          >
            <g class="about-pattern-books">
              <path class="about-shelf-line" d="M40 236h340M54 257h286" />
              <rect x="54" y="207" width="88" height="21" rx="5" transform="rotate(-3 54 207)" />
              <rect x="65" y="184" width="104" height="20" rx="5" transform="rotate(2 65 184)" />
              <rect x="47" y="161" width="78" height="19" rx="5" transform="rotate(-1 47 161)" />
              <rect x="231" y="214" width="107" height="22" rx="5" transform="rotate(2 231 214)" />
              <rect x="247" y="191" width="91" height="19" rx="5" transform="rotate(-2 247 191)" />
              <path d="M69 214h61M78 191h77M61 168h51M246 221h77M259 198h66" />
              <path class="about-bookmark" d="M284 191v-25l10 6 10-6v25" />
            </g>

            <g class="about-pattern-cat about-pattern-cat-caramel">
              <path class="about-cat-tail" d="M367 222c30 4 42-20 25-35-13-12 0-28 12-18" />
              <path class="about-cat-body" d="M301 211c4-43 17-68 44-72 30-4 48 24 54 72Z" />
              <path class="about-cat-ear" d="M315 120 305 82l31 22M380 120l15-35-34 21" />
              <circle class="about-cat-head" cx="354" cy="133" r="40" />
              <path class="about-cat-patch" d="M328 128c6-23 23-36 46-33 8 15 9 29 2 42-18 9-35 7-48-9Z" />
              <path class="about-cat-face" d="M338 132h10M365 132h10M350 143l6 5-10 0ZM344 153c5 4 10 4 15 0M334 145l-17-4M339 152l-16 5M376 145l16-4M372 152l15 5" />
              <path class="about-cat-stripe" d="M345 106c5-6 12-8 18-7M342 114c6-3 11-3 16-1" />
            </g>

            <g class="about-pattern-cat about-pattern-cat-mono">
              <path class="about-cat-tail" d="M97 199c-27 1-35-20-20-32 10-8 4-21-8-14" />
              <path class="about-cat-body" d="M102 205c-3-31-13-50-32-52-22-2-34 18-38 52Z" />
              <path class="about-cat-ear" d="m41 139-4-29 22 17M87 139l10-27-24 16" />
              <circle class="about-cat-head" cx="63" cy="145" r="28" />
              <path class="about-cat-patch" d="M45 140c5-15 18-23 35-20 5 10 4 20-2 28-13 5-25 3-33-8Z" />
              <path class="about-cat-face" d="M52 144h8M71 144h8M61 152l4 4-8 0ZM56 160c4 3 8 3 12 0" />
            </g>

            <g class="about-pattern-doodles">
              <path d="M154 53h58l14 17-14 17h-38l-13 10 3-11h-10Z" />
              <path d="M171 70h28M171 79h18" />
              <path d="m397 58 3 10 10 3-10 3-3 10-3-10-10-3 10-3Z" />
              <path d="m32 103 2 7 7 2-7 2-2 7-2-7-7-2 7-2Z" />
              <path d="M146 250c14-10 28-10 42 0M205 253c10-7 20-7 30 0" />
              <g class="about-paw-prints">
                <ellipse cx="203" cy="118" rx="8" ry="10" />
                <circle cx="194" cy="104" r="3.5" />
                <circle cx="202" cy="100" r="3.5" />
                <circle cx="211" cy="103" r="3.5" />
                <ellipse cx="226" cy="145" rx="7" ry="9" />
                <circle cx="218" cy="133" r="3" />
                <circle cx="226" cy="130" r="3" />
                <circle cx="234" cy="133" r="3" />
              </g>
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
