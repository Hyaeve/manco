<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ExternalLink, Loader2, LogIn, UserPlus } from 'lucide-vue-next'
import { api } from '../api'
import MancoBrand from '../components/MancoBrand.vue'
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
const featureMode = ref('bangumi')
const bangumiIndex = ref(0)
const bangumiDirection = ref('next')
let bangumiTimer = 0

const activeBangumi = computed(() => bangumiItems.value[bangumiIndex.value] || null)
const featureEyebrow = computed(() => featureMode.value === 'subscription'
  ? '订阅计划 · 作品推荐'
  : 'Bangumi · 随机推荐')
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
  let items = []
  try {
    const payload = await api.loginBangumi()
    items = Array.isArray(payload.items) ? payload.items : []
  } catch {
    items = []
  }
  if (items.length) {
    featureMode.value = 'bangumi'
  } else {
    try {
      const fallback = await api.loginSubscriptions()
      items = Array.isArray(fallback.items) ? fallback.items : []
    } catch {
      items = []
    }
    featureMode.value = items.length ? 'subscription' : 'fallback'
  }
  bangumiItems.value = items
  bangumiIndex.value = 0
  preloadBangumiCover(0)
  preloadBangumiCover(1)
  startBangumiRotation()
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
  <div
    class="login-page split-layout"
    :class="{ 'has-bangumi': featureMode === 'bangumi' && activeBangumi }"
  >
    <div class="login-bangumi-scene" aria-hidden="true">
      <Transition v-if="activeBangumi" appear :name="`login-bangumi-${bangumiDirection}`">
        <div :key="activeBangumi.id" class="login-bangumi-backdrop" :style="bangumiBackground" />
      </Transition>
      <div v-if="activeBangumi" class="login-bangumi-shade" />
    </div>
    <Transition :name="`login-bangumi-copy-${bangumiDirection}`" mode="out-in">
      <div
        v-if="activeBangumi"
        :key="activeBangumi.id"
        class="login-bangumi-feature"
      >
        <span>{{ featureEyebrow }}</span>
        <strong>{{ activeBangumi.title }}</strong>
        <p>{{ activeBangumi.summary }}</p>
      </div>
    </Transition>
    <Transition :name="`login-bangumi-copy-${bangumiDirection}`" mode="out-in">
      <a
        v-if="activeBangumi?.url"
        :key="activeBangumi.id"
        class="login-bangumi-link"
        :href="activeBangumi.url"
        target="_blank"
        rel="noopener noreferrer"
      >
        <ExternalLink :size="13" />
        <span>{{ activeBangumi.url }}</span>
      </a>
    </Transition>
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
      <MancoBrand class="login-brand" />
      <div v-if="setupRequired" class="login-title">
        <h1>创建账号</h1>
        <span>首次使用</span>
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
