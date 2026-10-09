<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Loader2, LogIn } from 'lucide-vue-next'
import { useAuthStore } from '../stores/auth'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const username = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)

async function submit() {
  error.value = ''
  if (!username.value || !password.value) {
    error.value = '请输入用户名和密码'
    return
  }
  loading.value = true
  try {
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
      <div class="brand" style="padding: 0; margin-bottom: 18px">
        <span class="brand-mark">M</span>
        <span class="brand-text">
          <strong>Manco</strong>
          <span>漫画订阅下载</span>
        </span>
      </div>
      <h1>登录</h1>
      <p>使用管理员账号进入，首次启动的默认账号见部署文档。</p>
      <form class="login-form" @submit.prevent="submit">
        <label class="field">
          <span>用户名</span>
          <input v-model="username" class="input" autocomplete="username" placeholder="admin" />
        </label>
        <label class="field">
          <span>密码</span>
          <input
            v-model="password"
            class="input"
            type="password"
            autocomplete="current-password"
            placeholder="请输入密码"
          />
        </label>
        <div v-if="error" class="alert error" style="margin: 0">{{ error }}</div>
        <button class="btn" type="submit" :disabled="loading">
          <Loader2 v-if="loading" :size="16" class="spin" />
          <LogIn v-else :size="16" />
          {{ loading ? '登录中' : '登录' }}
        </button>
      </form>
    </div>
  </div>
</template>
