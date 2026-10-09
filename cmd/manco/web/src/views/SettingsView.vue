<script setup>
import { onMounted, reactive, ref } from 'vue'
import { Clock, Download, KeyRound, Loader2, Network, Save, ShieldCheck } from 'lucide-vue-next'
import { api } from '../api'
import { useAuthStore } from '../stores/auth'
import PasswordInput from '../components/PasswordInput.vue'

const auth = useAuthStore()
const loading = ref(true)
const saving = ref('')
const saveState = reactive({})
const error = ref('')
const message = ref('')
const form = reactive({
  username: '',
  currentPassword: '',
  newPassword: '',
  confirmPassword: '',
  sessionTtlDays: 30,
  proxy: '',
  proxyUsername: '',
  proxyPassword: '',
  sourceConcurrency: { picacg: 1, jmcomic: 1, baozimh: 1 },
  maxPageConcurrency: 4,
  batchSize: 0,
  batchIntervalMinutes: 0,
  convertToSimplified: false,
  staleDays: { picacg: 0, jmcomic: 0, baozimh: 0 },
})

const sourceLabels = { picacg: '哔咔漫画', jmcomic: '禁漫天堂', baozimh: '包子漫画' }

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const payload = await api.settings()
    form.username = payload.username || auth.user?.username || ''
    form.sessionTtlDays = payload.sessionTtlDays || 30
    form.proxy = payload.proxy || ''
    form.proxyUsername = payload.proxyUsername || ''
    form.proxyPassword = payload.proxyPassword || ''
    form.maxPageConcurrency = payload.maxPageConcurrency || 4
    form.batchSize = payload.batchSize || 0
    form.batchIntervalMinutes = payload.batchIntervalMinutes || 0
    form.convertToSimplified = Boolean(payload.convertToSimplified)
    form.sourceConcurrency = { picacg: 1, jmcomic: 1, baozimh: 1, ...(payload.sourceConcurrency || {}) }
    form.staleDays = { picacg: 0, jmcomic: 0, baozimh: 0, ...(payload.staleDays || {}) }
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

async function save(section, label) {
  saving.value = section
  error.value = ''
  message.value = ''
  if ((section === 'account' || section === 'all') && form.newPassword && form.newPassword !== form.confirmPassword) {
    error.value = '两次输入的新密码不一致'
    saving.value = ''
    return
  }
  try {
    await api.saveSettings({
      username: form.username,
      currentPassword: form.currentPassword,
      newPassword: form.newPassword,
      sessionTtlDays: Number(form.sessionTtlDays) || 30,
      proxy: form.proxy,
      proxyUsername: form.proxyUsername,
      proxyPassword: form.proxyPassword,
      sourceConcurrency: {
        picacg: Number(form.sourceConcurrency.picacg) || 1,
        jmcomic: Number(form.sourceConcurrency.jmcomic) || 1,
        baozimh: Number(form.sourceConcurrency.baozimh) || 1,
      },
      maxPageConcurrency: Number(form.maxPageConcurrency) || 4,
      batchSize: Number(form.batchSize) || 0,
      batchIntervalMinutes: Number(form.batchIntervalMinutes) || 0,
      convertToSimplified: Boolean(form.convertToSimplified),
      staleDays: {
        picacg: Number(form.staleDays.picacg) || 0,
        jmcomic: Number(form.staleDays.jmcomic) || 0,
        baozimh: Number(form.staleDays.baozimh) || 0,
      },
    })
    if (form.newPassword) {
      form.currentPassword = ''
      form.newPassword = ''
      form.confirmPassword = ''
      if (auth.user) auth.user = { ...auth.user, username: form.username }
    }
    saveState[section] = true
    window.setTimeout(() => {
      saveState[section] = false
    }, 2000)
    message.value = `${label}已保存`
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    saving.value = ''
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

  <div v-else class="settings-sections">
    <section class="card card-pad">
      <div class="section-head">
        <div class="inline">
          <ShieldCheck :size="17" />
          <h2>账号与安全</h2>
        </div>
      </div>
      <div class="settings-grid">
        <label class="field">
          <span>账号</span>
          <input v-model="form.username" class="input" autocomplete="username" />
        </label>
        <label class="field">
          <span>会话存活期（天）</span>
          <input v-model.number="form.sessionTtlDays" class="input" type="number" min="1" max="3650" />
        </label>
      </div>
      <div class="settings-grid">
        <label class="field">
          <span>当前密码</span>
          <PasswordInput v-model="form.currentPassword" autocomplete="current-password" />
        </label>
        <label class="field">
          <span>新密码</span>
          <PasswordInput v-model="form.newPassword" autocomplete="new-password" />
        </label>
      </div>
      <label class="field">
        <span>确认新密码</span>
        <PasswordInput v-model="form.confirmPassword" autocomplete="new-password" />
      </label>
      <div class="inline settings-actions">
        <button class="btn" type="button" :disabled="saving === 'account'" @click="save('account', '账号与安全设置')">
          <Loader2 v-if="saving === 'account'" :size="15" class="spin" />
          <KeyRound v-else :size="15" />
          保存账号与安全
        </button>
        <span v-if="saveState.account" class="badge success">已保存</span>
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
        <button class="btn" type="button" :disabled="saving === 'proxy'" @click="save('proxy', '代理设置')">
          <Loader2 v-if="saving === 'proxy'" :size="15" class="spin" />
          <Save v-else :size="15" />
          保存代理
        </button>
        <span v-if="saveState.proxy" class="badge success">已保存</span>
      </div>
    </section>

    <section class="card card-pad">
      <div class="section-head">
        <div class="inline">
          <Download :size="17" />
          <h2>下载设置</h2>
        </div>
      </div>
      <div class="settings-block">
        <h3>每个漫画源的下载线程</h3>
        <div class="settings-grid settings-grid-3">
          <label v-for="(label, id) in sourceLabels" :key="id" class="field">
            <span>{{ label }}</span>
            <input v-model.number="form.sourceConcurrency[id]" class="input" type="number" min="1" max="8" />
          </label>
        </div>
      </div>
      <div class="settings-grid settings-grid-3">
        <label class="field">
          <span>图片下载线程</span>
          <input v-model.number="form.maxPageConcurrency" class="input" type="number" min="1" max="16" />
        </label>
        <label class="field">
          <span>每下载 N 个文件后暂停</span>
          <input v-model.number="form.batchSize" class="input" type="number" min="0" max="1000" />
        </label>
        <label class="field">
          <span>暂停时长（分钟）</span>
          <input v-model.number="form.batchIntervalMinutes" class="input" type="number" min="0" max="10080" />
        </label>
      </div>
      <label class="switch-row">
        <input v-model="form.convertToSimplified" type="checkbox" />
        <span>下载时执行繁体转简体（不影响完成/失败记录）</span>
      </label>
      <div class="settings-block">
        <h3>超过多少天没有更新就关闭订阅</h3>
        <p class="muted small">留空或填 0 表示不启用；关闭 15 天无人重新启用后自动归档。</p>
        <div class="settings-grid settings-grid-3">
          <label v-for="(label, id) in sourceLabels" :key="id" class="field">
            <span>{{ label }}</span>
            <input v-model.number="form.staleDays[id]" class="input" type="number" min="0" max="3650" placeholder="0" />
          </label>
        </div>
      </div>
      <div class="inline settings-actions">
        <button class="btn" type="button" :disabled="saving === 'download'" @click="save('download', '下载设置')">
          <Loader2 v-if="saving === 'download'" :size="15" class="spin" />
          <Download v-else :size="15" />
          保存下载设置
        </button>
        <span v-if="saveState.download" class="badge success">已保存</span>
      </div>
    </section>

    <section class="card card-pad muted small">
      <div class="inline">
        <Clock :size="15" />
        <span>订阅会在各自 cron 时间检查更新；下载失败后 10 分钟、30 分钟各自动重试一次，之后保留失败记录等待手动重试。</span>
      </div>
    </section>
  </div>
</template>
