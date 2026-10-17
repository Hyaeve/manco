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
  convertToSimplified: false,
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
        <!-- 回车即保存：keydown 会从内部输入框冒泡到这里。 -->
        <div class="settings-form-stack" @keydown.enter.prevent="saveAccount">
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
      </section>

    </div>

    <div class="settings-pair settings-bottom-pair">
      <section id="settings-logs" class="card card-pad settings-compact-card">
        <div class="settings-card-title">
          <div class="inline">
            <RefreshCw :size="17" />
            <h2>运行日志</h2>
          </div>
          <button class="btn settings-save-button" type="button" :disabled="saving === 'logs'" @click="saveLogs">
            <Loader2 v-if="saving === 'logs'" :size="15" class="spin" />
            保存
          </button>
        </div>
        <p class="muted small settings-card-help">
          内存中保留最近 N 条日志，超出上限丢弃最旧的；保存天数用于日志落盘后的清理，两者同时生效。
        </p>
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
      </section>
    </div>

    <div class="settings-pair settings-bottom-pair">
      <section id="settings-download" class="card card-pad settings-compact-card">
        <div class="settings-card-title">
          <div class="inline">
            <Download :size="17" />
            <h2>下载设置</h2>
          </div>
          <button class="btn settings-save-button" type="button" :disabled="saving === 'download'" @click="saveDownload">
            <Loader2 v-if="saving === 'download'" :size="15" class="spin" />
            保存
          </button>
        </div>
        <p class="muted small settings-card-help">
          「全局同时下载章节数」是整台实例的上限：不管有多少内容源在排队，同时下载的章节都不会超过这个数。单个内容源可以在「资源拓展」里单独限制得更低。
        </p>
        <div class="settings-form-stack" @keydown.enter.prevent="saveDownload">
          <label class="field">
            <span>全局同时下载章节数</span>
            <input v-model.number="form.maxChapterConcurrency" class="input" type="number" min="1" max="32" />
          </label>
          <label class="field">
            <span>每章图片并发</span>
            <input v-model.number="form.maxPageConcurrency" class="input" type="number" min="1" max="32" />
          </label>
          <div class="archive-number-row">
            <span>每下载</span>
            <input v-model.number="form.batchSize" class="input archive-number" type="number" min="0" max="1000" />
            <span>章后暂停</span>
            <input v-model.number="form.batchIntervalMinutes" class="input archive-number" type="number" min="0" max="1440" />
            <span>分钟</span>
          </div>
          <p class="muted small">暂停按内容源全局生效，队列里其它内容源不受影响；填 0 表示不暂停。</p>
        </div>
      </section>
      <section id="settings-telegram" class="card card-pad settings-compact-card">
        <div class="settings-card-title">
          <div class="inline">
            <Send :size="17" />
            <h2>TG 通知</h2>
          </div>
          <button class="btn settings-save-button" type="button" :disabled="saving === 'telegram'" @click="saveTelegram">
            <Loader2 v-if="saving === 'telegram'" :size="15" class="spin" />
            保存
          </button>
        </div>
        <p class="muted small settings-card-help">
          在 @BotFather 创建机器人拿到 Token，把聊天 ID 填进允许列表后即可接收例行通知，也能直接用指令查看订阅、搜索并订阅作品。网络走「代理」栏目里的系统代理设置。
        </p>
        <div class="settings-form-stack" @keydown.enter.prevent="saveTelegram">
          <label class="field">
            <span>Bot Token</span>
            <input v-model="form.telegramToken" class="input" autocomplete="off" placeholder="123456:ABC-DEF..." />
          </label>
          <label class="field">
            <span>允许的 Chat ID</span>
            <input v-model="form.telegramChatIds" class="input" autocomplete="off" placeholder="多个用逗号分隔，例如 -1001234567890, 123456789" />
          </label>
          <button
            class="function-toggle"
            :class="{ active: form.telegramEnabled }"
            type="button"
            @click="form.telegramEnabled = !form.telegramEnabled"
          >
            <Send :size="16" />
            <span>启用 TG 通知</span>
          </button>
        </div>
      </section>
    </div>

    <div class="settings-pair settings-bottom-pair">
      <section id="settings-archive" class="card card-pad settings-compact-card">
        <div class="settings-card-title">
          <div class="inline">
            <Archive :size="17" />
            <h2>订阅归档与繁转简</h2>
          </div>
          <Loader2 v-if="saving === 'archive' || saving === 'convert'" :size="16" class="spin muted" />
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
        <div id="settings-convert" class="settings-feature-group">
          <div class="inline settings-feature-heading">
            <Languages :size="17" />
            <h3>繁转简</h3>
          </div>
          <p class="muted small settings-card-help">
            开启后探索发现、搜索结果和作品详情中的名称、作者、简介与标签统一显示为简体中文，原始数据与本地缓存不会被改写。
          </p>
          <label class="switch-row">
            <input v-model="form.convertToSimplified" type="checkbox" @change="saveConvertToSimplified" />
            <span>全局繁转简</span>
          </label>
        </div>
      </section>

      <section id="settings-about" class="card card-pad settings-compact-card settings-about-card">
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
