<script setup>
import { useRouter } from 'vue-router'

const props = defineProps({
  to: {
    type: [String, Object],
    required: true,
  },
})

const router = useRouter()

function navigate() {
  router.push(props.to)
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
  navigate()
}
</script>

<template>
  <div
    class="content-link"
    role="link"
    tabindex="0"
    @click="handleClick"
    @keydown.enter.prevent="navigate"
    @keydown.space.prevent="navigate"
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
