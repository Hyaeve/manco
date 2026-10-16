<script setup>
import { useRouter } from 'vue-router'

const props = defineProps({
  to: {
    type: [String, Object],
    required: true,
  },
  // 可选守卫：返回 false 时阻止本次跳转（用于“先解锁再进入”之类的交互）。
  beforeNavigate: {
    type: Function,
    default: null,
  },
})

const router = useRouter()

function navigate() {
  router.push(props.to)
}

function guard(event) {
  if (typeof props.beforeNavigate !== 'function') return true
  return props.beforeNavigate(event) !== false
}

function handleClick(event) {
  if (
    event.defaultPrevented ||
    event.button !== 0 ||
    event.metaKey ||
    event.ctrlKey ||
    event.shiftKey ||
    event.altKey
  ) {
    return
  }
  if (!guard(event)) return
  navigate()
}

function handleKeydown(event) {
  if (!guard(event)) return
  navigate()
}
</script>

<template>
  <div
    class="content-link"
    role="link"
    tabindex="0"
    @click="handleClick"
    @keydown.enter.prevent="handleKeydown"
    @keydown.space.prevent="handleKeydown"
  >
    <slot />
  </div>
</template>

<style scoped>
.content-link {
  cursor: pointer;
}

.content-link:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 2px;
}
</style>
