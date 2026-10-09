<script setup>
import { onMounted, ref } from 'vue'
import { Database, FolderOpen, Gauge, KeyRound, Loader2, Network, RefreshCw, Save, Server, Timer } from 'lucide-vue-next'
import { api } from '../api'
import { useAuthStore } from '../stores/auth'

const auth = useAuthStore()
const settings = ref(null)
const repoUrl = ref('')
const proxy = ref('')
const username = ref('')
const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const loading = ref(true)
const saving = ref(false)
const error = ref('')
const message = ref('')

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const payload = await api.settings()
    settings.value = payload
    repoUrl.value = payload.repoUrl || ''
    proxy.value = payload.proxy || ''
    username.value = payload.username || auth.user?.username || ''
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  error.value = ''
  message.value = ''
  try {
    if (newPassword.value && newPassword.value !== confirmPassword.value) {
      error.value = '两次输入的新密码不一致'
      saving.value = false
      return
    }
    const payload = await api.saveSettings({
      username: username.value,
      repoUrl: repoUrl.value,
      proxy: proxy.value,
      currentPassword: currentPassword.value,
      newPassword: newPassword.value,
      cookieSecure: settings.value.cookieSecure,
    })
    settings.value = { ...settings.value, repoUrl: payload.repoUrl, proxy: payload.proxy }
    username.value = payload.username || username.value
    if (auth.user) auth.user = { ...auth.user, username: payload.username || auth.user.username }
    proxy.value = payload.proxy || ''
    currentPassword.value = ''
    newPassword.value = ''
    confirmPassword.value = ''
    message.value = '设置已保存。'
  } catch (err) {
    error.value = err.message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div v-if="error" class="alert error">{{ error }}</div>
  <div v-if="message" class="alert ok">{{ message }}</div>

  <div v-if="loading" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载设置</span>
  </div>
  <template v-else-if="settings">
    <section class="card card-pad" style="margin-bottom: 18px">
      <div class="section-head">
        <div class="inline">
          <Server :size="17" />
          <h2>漫画源拓展仓库</h2>
        </div>
      </div>
      <label class="field" style="margin-bottom: 12px">
        <span>index.min.json 地址</span>
        <input v-model="repoUrl" class="input" placeholder="https://raw.githubusercontent.com/..." />
      </label>
      <div class="inline">
        <button class="btn" type="button" :disabled="saving" @click="save">
          <Loader2 v-if="saving" :size="15" class="spin" />
          <Save v-else :size="15" />
          保存
        </button>
        <button class="btn secondary" type="button" :disabled="loading" @click="load">
          <RefreshCw :size="15" />
          重新读取
        </button>
      </div>
    </section>

    <section class="stat-grid">
      <div class="card stat">
        <span class="stat-icon"><FolderOpen :size="18" /></span>
        <span>
          <span class="stat-label">下载目录</span>
          <span class="stat-value" style="font-size: 14px; word-break: break-all">{{ settings.downloadDir }}</span>
        </span>
      </div>
      <div class="card stat">
        <span class="stat-icon"><Database :size="18" /></span>
        <span>
          <span class="stat-label">数据目录</span>
          <span class="stat-value" style="font-size: 14px; word-break: break-all">{{ settings.dataDir }}</span>
        </span>
      </div>
      <div class="card stat">
        <span class="stat-icon"><Timer :size="18" /></span>
        <span>
          <span class="stat-label">订阅扫描间隔</span>
          <span class="stat-value" style="font-size: 14px">{{ settings.scanInterval }}</span>
        </span>
      </div>
      <div class="card stat">
        <span class="stat-icon"><Gauge :size="18" /></span>
        <span>
          <span class="stat-label">并发（章节 / 图片）</span>
          <span class="stat-value" style="font-size: 14px">
            {{ settings.maxChapterConcurrency }} / {{ settings.maxPageConcurrency }}
          </span>
        </span>
      </div>
    </section>

    <section class="card card-pad" style="margin-bottom: 18px">
      <div class="section-head">
        <div class="inline">
          <Network :size="17" />
          <h2>网络代理</h2>
        </div>
      </div>
      <label class="field">
        <span>HTTP / HTTPS 代理地址</span>
        <input v-model="proxy" class="input" placeholder="http://127.0.0.1:7890" />
      </label>
      <div class="inline" style="margin-top: 12px">
        <button class="btn" type="button" :disabled="saving" @click="save">
          <Loader2 v-if="saving" :size="15" class="spin" />
          <Save v-else :size="15" />
          保存
        </button>
      </div>
    </section>

    <section class="card card-pad">
      <div class="section-head" style="margin-bottom: 10px">
        <div class="inline">
          <KeyRound :size="17" />
          <h2>账号设置</h2>
        </div>
      </div>
      <div class="field" style="margin-bottom: 10px">
        <span>用户名</span>
        <input v-model="username" class="input" autocomplete="username" />
      </div>
      <p class="muted small" style="margin: 0 0 12px">修改密码需填写当前密码；只改用户名时密码留空即可。</p>
      <div class="field" style="margin-bottom: 10px">
        <span>当前密码</span>
        <input v-model="currentPassword" class="input" type="password" autocomplete="current-password" />
      </div>
      <div class="field" style="margin-bottom: 10px">
        <span>新密码</span>
        <input v-model="newPassword" class="input" type="password" autocomplete="new-password" />
      </div>
      <div class="field">
        <span>确认新密码</span>
        <input v-model="confirmPassword" class="input" type="password" autocomplete="new-password" />
      </div>
      <div class="inline" style="margin-top: 12px">
        <button class="btn" type="button" :disabled="saving" @click="save">
          <Loader2 v-if="saving" :size="15" class="spin" />
          <Save v-else :size="15" />
          保存账号设置
        </button>
      </div>
    </section>
  </template>
</template>
