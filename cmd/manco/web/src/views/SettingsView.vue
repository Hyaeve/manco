<script setup>
import { onMounted, ref } from 'vue'
import { Database, FolderOpen, Gauge, Loader2, RefreshCw, Save, Server, Timer, User } from 'lucide-vue-next'
import { api } from '../api'
import { useAuthStore } from '../stores/auth'

const auth = useAuthStore()
const settings = ref(null)
const repoUrl = ref('')
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
    const payload = await api.saveSettings({ repoUrl: repoUrl.value })
    settings.value = { ...settings.value, repoUrl: payload.repoUrl }
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

    <section class="card card-pad">
      <div class="section-head" style="margin-bottom: 10px">
        <div class="inline">
          <User :size="17" />
          <h2>账号</h2>
        </div>
      </div>
      <p class="muted small" style="margin: 0">
        当前登录：{{ auth.user?.username }}。管理员账号由环境变量
        <code>MANCO_ADMIN_USER</code> / <code>MANCO_ADMIN_PASSWORD</code> 在首次启动时创建。
      </p>
      <p class="muted small" style="margin: 8px 0 0">
        每个章节都会下载为独立 CBZ：下载目录/作品名/章节名.cbz。
      </p>
    </section>
  </template>
</template>
