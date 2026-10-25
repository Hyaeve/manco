<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
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
const bangumiItems = ref([])
const bangumiIndex = ref(0)
const bangumiDirection = ref('next')
let bangumiTimer = 0

const activeBangumi = computed(() => bangumiItems.value[bangumiIndex.value] || null)
const bangumiBackground = computed(() => activeBangumi.value?.cover
  ? { backgroundImage: `url("${activeBangumi.value.cover}")` }
  : {})

onMounted(async () => {
  loadBangumiBackground()
  try {
    const payload = await api.setup()
    setupRequired.value = Boolean(payload.setupRequired)
  } catch {
    setupRequired.value = false
  }
})

onUnmounted(() => {
  if (bangumiTimer) window.clearInterval(bangumiTimer)
})

async function loadBangumiBackground() {
  try {
    const payload = await api.loginBangumi()
    bangumiItems.value = Array.isArray(payload.items) ? payload.items : []
    bangumiIndex.value = 0
    preloadBangumiCover(0)
    preloadBangumiCover(1)
    startBangumiRotation()
  } catch {
    bangumiItems.value = []
  }
}

function startBangumiRotation() {
  if (bangumiTimer) window.clearInterval(bangumiTimer)
  bangumiTimer = 0
  if (bangumiItems.value.length <= 1) return
  bangumiTimer = window.setInterval(() => {
    bangumiDirection.value = 'next'
    const nextIndex = (bangumiIndex.value + 1) % bangumiItems.value.length
    preloadBangumiCover(nextIndex + 1)
    bangumiIndex.value = nextIndex
  }, 12000)
}

function selectBangumi(index) {
  if (index === bangumiIndex.value || !bangumiItems.value[index]) return
  bangumiDirection.value = index < bangumiIndex.value ? 'prev' : 'next'
  bangumiIndex.value = index
  preloadBangumiCover(index + 1)
  startBangumiRotation()
}

function preloadBangumiCover(index) {
  const cover = bangumiItems.value[index % bangumiItems.value.length]?.cover
  if (!cover) return
  const image = new window.Image()
  image.decoding = 'async'
  image.src = cover
}

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
    const target = typeof route.query.redirect === 'string' ? route.query.redirect : '/dashboard'
    router.push(target)
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page" :class="{ 'has-bangumi': activeBangumi }">
    <div v-if="activeBangumi" class="login-bangumi-scene" aria-hidden="true">
      <Transition :name="`login-bangumi-${bangumiDirection}`">
        <div :key="activeBangumi.id" class="login-bangumi-backdrop" :style="bangumiBackground" />
      </Transition>
      <div class="login-bangumi-shade" />
    </div>
    <a
      v-if="activeBangumi"
      class="login-bangumi-feature"
      :href="activeBangumi.url"
      target="_blank"
      rel="noopener noreferrer"
    >
      <span>Bangumi · 随机推荐</span>
      <strong>{{ activeBangumi.title }}</strong>
      <p>{{ activeBangumi.summary }}</p>
    </a>
    <div
      v-if="activeBangumi && bangumiItems.length > 1"
      class="login-bangumi-dots"
      aria-label="Bangumi 近期注目切换"
    >
      <button
        v-for="(item, index) in bangumiItems"
        :key="item.id"
        type="button"
        :class="{ active: index === bangumiIndex }"
        :aria-label="`切换到 ${item.title}`"
        @mouseenter="selectBangumi(index)"
        @focus="selectBangumi(index)"
        @click="selectBangumi(index)"
      />
    </div>
    <div class="login-card">
      <MancoLogo class="login-logo" :size="72" />
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
