<script setup>
import { computed, ref, watch } from 'vue'
import { BookOpenText, Clapperboard } from 'lucide-vue-next'
import ComicSourceIcon from './ComicSourceIcon.vue'

const props = defineProps({
  src: { type: String, default: '' },
  kind: { type: String, default: 'comic' },
  alt: { type: String, default: '' },
  size: { type: [Number, String], default: 22 },
})

const failed = ref(false)
watch(
  () => props.src,
  () => {
    failed.value = false
  },
)

// 图标下载失败时按内容源类型回退，绝不显示图裂。
const fallbackIcon = computed(() => {
  if (props.kind === 'book') return BookOpenText
  if (props.kind === 'other') return Clapperboard
  return ComicSourceIcon
})
</script>

<template>
  <img v-if="src && !failed" :src="src" :alt="alt" loading="lazy" @error="failed = true" />
  <component :is="fallbackIcon" v-else :size="Number(size)" :aria-label="alt || undefined" />
</template>
