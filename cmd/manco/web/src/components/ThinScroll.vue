<script setup>
import { nextTick, onMounted, onUnmounted, ref } from 'vue'

const props = defineProps({
  contentClass: { type: String, default: '' },
  thickness: { type: Number, default: 1 },
})

const emit = defineEmits(['scroll'])
const area = ref(null)
const thumb = ref(null)
const visible = ref(false)
let observer = null
let mutations = null
let frame = 0
let drag = null

defineExpose({
  element: area,
  update,
  scrollToTop: () => {
    if (area.value) area.value.scrollTop = 0
  },
})

function update() {
  cancelAnimationFrame(frame)
  frame = requestAnimationFrame(() => {
    const el = area.value
    if (!el) return
    const height = el.clientHeight
    const total = el.scrollHeight
    visible.value = total > height + 1
    const size = Math.min(height, Math.max(24, (height * height) / total))
    const top = total > height ? (el.scrollTop / (total - height)) * (height - size) : 0
    const scale = (window.devicePixelRatio || 1) * (window.visualViewport?.scale || 1)
    thumb.value?.style.setProperty('--hairline', `${props.thickness / scale}px`)
    if (thumb.value) {
      thumb.value.style.height = `${size}px`
      thumb.value.style.transform = `translateY(${top}px)`
    }
  })
}

function onScroll() {
  update()
  emit('scroll', area.value?.scrollTop || 0)
}

function start(event) {
  const el = area.value
  drag = { y: event.clientY, top: el.scrollTop }
  event.currentTarget.setPointerCapture(event.pointerId)
}

function move(event) {
  if (!drag || !area.value || !thumb.value) return
  const el = area.value
  const travel = el.clientHeight - thumb.value.clientHeight
  if (travel > 0) {
    el.scrollTop = drag.top + ((event.clientY - drag.y) * (el.scrollHeight - el.clientHeight)) / travel
  }
}

onMounted(async () => {
  await nextTick()
  const el = area.value
  if (!el) return
  observer = new ResizeObserver(update)
  observer.observe(el)
  const observeChildren = () => {
    for (const child of el.children) observer.observe(child)
    update()
  }
  mutations = new MutationObserver(observeChildren)
  mutations.observe(el, { childList: true, subtree: true, characterData: true })
  observeChildren()
  window.addEventListener('resize', update)
  window.visualViewport?.addEventListener('resize', update)
})

onUnmounted(() => {
  observer?.disconnect()
  mutations?.disconnect()
  cancelAnimationFrame(frame)
  window.removeEventListener('resize', update)
  window.visualViewport?.removeEventListener('resize', update)
})
</script>

<template>
  <div class="thin-scroll">
    <div
      ref="area"
      class="thin-scroll-area"
      :class="props.contentClass"
      tabindex="0"
      @scroll.passive="onScroll"
    >
      <slot />
    </div>
    <div v-show="visible" class="thin-scroll-rail" aria-hidden="true">
      <div
        ref="thumb"
        class="thin-scroll-thumb"
        @pointerdown.prevent="start"
        @pointermove="move"
        @pointerup="drag = null"
        @pointercancel="drag = null"
        @lostpointercapture="drag = null"
      />
    </div>
  </div>
</template>

<style scoped>
.thin-scroll {
  position: relative;
  min-height: 0;
}
.thin-scroll-area {
  height: 100%;
  overflow: auto;
  scrollbar-width: none !important;
}
.thin-scroll-area::-webkit-scrollbar {
  display: none !important;
}
.thin-scroll-rail {
  position: absolute;
  inset: 0 0 0 auto;
  width: 8px;
  pointer-events: none;
  z-index: 2;
}
.thin-scroll-thumb {
  width: 8px;
  position: absolute;
  right: 0;
  top: 0;
  pointer-events: auto;
  touch-action: none;
}
.thin-scroll-thumb::after {
  content: '';
  position: absolute;
  right: 1px;
  inset-block: 0;
  width: var(--hairline, 1px);
  border-radius: 4px;
  background: color-mix(in srgb, var(--primary) 30%, transparent);
}
.thin-scroll-thumb:hover::after {
  background: color-mix(in srgb, var(--primary) 60%, transparent);
}
</style>
