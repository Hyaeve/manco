<script setup>
import { computed, nextTick, onActivated, onDeactivated, onMounted, onUnmounted, ref, watch } from 'vue'

const props = defineProps({
  items: { type: Array, required: true },
  itemKey: { type: String, default: 'path' },
})
const emit = defineEmits(['near-end'])
const grid = ref(null)
const columns = ref(1)
const rowHeight = ref(320)
const gap = ref(14)
const scrollOffset = ref(0)
const viewportHeight = ref(720)
const step = computed(() => rowHeight.value + gap.value)
const rows = computed(() => Math.ceil(props.items.length / columns.value))
const startRow = computed(() => Math.min(Math.max(0, rows.value - 1), Math.max(0, Math.floor(scrollOffset.value / step.value) - 2)))
const endRow = computed(() => Math.min(rows.value, startRow.value + Math.ceil(viewportHeight.value / step.value) + 5))
const entries = computed(() => props.items.slice(startRow.value * columns.value, endRow.value * columns.value)
  .map((item, offset) => ({ item, index: startRow.value * columns.value + offset })))
const before = computed(() => Math.max(0, startRow.value * step.value - gap.value))
const after = computed(() => Math.max(0, (rows.value - endRow.value) * step.value - gap.value))
let container = null
let observer = null
let frame = 0
let active = true

function measure() {
  if (!active || !grid.value) return
  const style = getComputedStyle(grid.value)
  columns.value = Math.max(1, style.gridTemplateColumns.split(/\s+/).filter(Boolean).length)
  gap.value = Number.parseFloat(style.rowGap) || 0
  const cell = grid.value.querySelector('.windowed-grid-cell')
  if (cell?.offsetHeight) rowHeight.value = cell.offsetHeight
  const top = grid.value.getBoundingClientRect().top - (container?.getBoundingClientRect().top || 0)
  scrollOffset.value = Math.max(0, -top)
  viewportHeight.value = container?.clientHeight || window.innerHeight
  if (endRow.value >= rows.value - 2) emit('near-end')
}

function scheduleMeasure() {
  if (!active || frame) return
  frame = requestAnimationFrame(() => {
    frame = 0
    measure()
  })
}

onMounted(() => {
  container = grid.value?.closest('.content-scroll')
  container?.addEventListener('scroll', scheduleMeasure, { passive: true })
  observer = new ResizeObserver(scheduleMeasure)
  if (grid.value) observer.observe(grid.value)
  measure()
})
onActivated(() => { active = true; scheduleMeasure() })
onDeactivated(() => {
  active = false
  if (frame) cancelAnimationFrame(frame)
  frame = 0
})
onUnmounted(() => {
  active = false
  container?.removeEventListener('scroll', scheduleMeasure)
  observer?.disconnect()
  if (frame) cancelAnimationFrame(frame)
})
watch(() => props.items, () => nextTick(scheduleMeasure))
</script>

<template>
  <div ref="grid">
    <div v-if="before" aria-hidden="true" class="windowed-grid-spacer" :style="{ height: `${before}px` }" />
    <div v-for="entry in entries" :key="entry.item[itemKey] || entry.index" class="windowed-grid-cell">
      <slot :item="entry.item" :index="entry.index" />
    </div>
    <div v-if="after" aria-hidden="true" class="windowed-grid-spacer" :style="{ height: `${after}px` }" />
  </div>
</template>

<style scoped>
.windowed-grid-spacer { grid-column: 1 / -1; pointer-events: none; }
.windowed-grid-cell { min-width: 0; }
.windowed-grid-cell > :deep(*) { width: 100%; }
</style>
