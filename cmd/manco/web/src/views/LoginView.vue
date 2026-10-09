<script setup>
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Loader2, LogIn, UserPlus } from 'lucide-vue-next'
import { api } from '../api'
import MancoLogo from '../components/MancoLogo.vue'
import { useAuthStore } from '../stores/auth'
import PasswordInput from '../components/PasswordInput.vue'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const username = ref('')
const password = ref('')
const confirm = ref('')
const error = ref('')
const loading = ref(false)
const setupRequired = ref(false)

onMounted(async () => {
  try {
    const payload = await api.setup()
    setupRequired.value = Boolean(payload.setupRequired)
  } catch {
    setupRequired.value = false
  }
})

async function submit() {
  error.value = ''
  if (!username.value || !password.value) {
    error.value = '请输入用户名和密码'
    return
  }
  if (setupRequired.value && password.value !== confirm.value) {
    error.value = '两次输入的密码不一致'
    return
  }
  loading.value = true
  try {
    if (setupRequired.value) {
      await api.register({ username: username.value, password: password.value })
    }
    await auth.login(username.value, password.value)
    const target = typeof route.query.redirect === 'string' ? route.query.redirect : '/'
    router.push(target)
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <div class="login-card">
      <MancoLogo class="login-logo" :size="54" />
      <div class="login-title">
        <h1>{{ setupRequired ? '创建账号' : '登录' }}</h1>
        <span v-if="setupRequired">首次使用</span>
      </div>
      <form class="login-form" @submit.prevent="submit">
        <input
          v-model="username"
          class="input"
          autocomplete="username"
          aria-label="用户名"
          placeholder="用户名"
        />
        <PasswordInput
          v-model="password"
          :autocomplete="setupRequired ? 'new-password' : 'current-password'"
          placeholder="密码"
        />
        <PasswordInput
          v-if="setupRequired"
          v-model="confirm"
          autocomplete="new-password"
          placeholder="确认密码"
        />
        <div v-if="error" class="alert error" style="margin: 0">{{ error }}</div>
        <button class="btn login-submit" type="submit" :disabled="loading">
          <Loader2 v-if="loading" :size="16" class="spin" />
          <UserPlus v-else-if="setupRequired" :size="16" />
          <LogIn v-else :size="16" />
          {{ loading ? '处理中' : setupRequired ? '创建并进入' : '登录' }}
        </button>
      </form>
    </div>
  </div>
</template>
