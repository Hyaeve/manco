<script setup>
import { computed, nextTick, onActivated, onDeactivated, onMounted, onUnmounted, ref, watch } from 'vue'

const props = defineProps({
  items: { type: Array, required: true },
  itemKey: { type: String, default: 'path' },
  total: { type: Number, default: -1 },
  offset: { type: Number, default: 0 },
  loadingRow: { type: Boolean, default: false },
})
const emit = defineEmits(['near-end', 'window-change'])
const grid = ref(null)
const columns = ref(1)
const rowHeight = ref(320)
const gap = ref(14)
const scrollOffset = ref(0)
const viewportHeight = ref(720)
const step = computed(() => rowHeight.value + gap.value)
const totalCount = computed(() => (props.total >= 0 ? props.total : props.items.length))
const contentRows = computed(() => Math.ceil(totalCount.value / columns.value))
const gridRows = computed(() => contentRows.value + (props.loadingRow && totalCount.value > 0 ? 1 : 0))
const loadedStartRow = computed(() => Math.floor(Math.max(0, props.offset) / columns.value))
const loadedEndRow = computed(() => Math.min(contentRows.value, loadedStartRow.value + Math.ceil(props.items.length / columns.value)))
const startRow = computed(() => Math.min(Math.max(0, gridRows.value - 1), Math.max(0, Math.floor(scrollOffset.value / step.value) - 2)))
const endRow = computed(() => Math.min(gridRows.value, startRow.value + Math.ceil(viewportHeight.value / step.value) + 5))
const showLoadingRow = computed(() => props.loadingRow && props.items.length > 0 && endRow.value >= gridRows.value)
const renderStartRow = computed(() => Math.min(contentRows.value, Math.max(startRow.value, loadedStartRow.value)))
const renderEndRow = computed(() => Math.min(endRow.value, loadedEndRow.value))
const localStart = computed(() => Math.max(0, (renderStartRow.value - loadedStartRow.value) * columns.value))
const localEnd = computed(() => Math.min(props.items.length, (renderEndRow.value - loadedStartRow.value) * columns.value))
const entries = computed(() => props.items.slice(localStart.value, localEnd.value)
  .map((item, offset) => ({ item, index: Math.max(0, props.offset) + localStart.value + offset })))
const before = computed(() => Math.max(0, renderStartRow.value * step.value - gap.value))
const after = computed(() => {
  if (showLoadingRow.value) return 0
  return Math.max(0, (gridRows.value - renderEndRow.value) * step.value - gap.value)
})
let container = null
let observer = null
let frame = 0
let active = true
let lastWindowRequest = ''

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
  if (endRow.value >= gridRows.value - 1) emit('near-end')
  if (props.total >= 0 && columns.value > 0) {
    const firstVisible = Math.floor(scrollOffset.value / step.value)
    const lastVisible = Math.ceil((scrollOffset.value + viewportHeight.value) / step.value)
    const wantedStart = Math.max(0, firstVisible - 1)
    const wantedEnd = Math.min(contentRows.value, Math.max(wantedStart + 1, lastVisible + 1))
    const start = wantedStart * columns.value
    const limit = Math.max(0, wantedEnd * columns.value - start)
    const signature = `${start}:${limit}:${totalCount.value}:${columns.value}`
    if (signature !== lastWindowRequest) {
      lastWindowRequest = signature
      emit('window-change', { start, limit, row: wantedStart, columns: columns.value })
    }
  }
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
watch(() => [props.items, props.total, props.offset, props.loadingRow], () => nextTick(scheduleMeasure))
</script>

<template>
  <div ref="grid">
    <div v-if="before" aria-hidden="true" class="windowed-grid-spacer" :style="{ height: `${before}px` }" />
    <div v-for="entry in entries" :key="entry.item[itemKey] || entry.index" class="windowed-grid-cell">
      <slot :item="entry.item" :index="entry.index" />
    </div>
    <div v-if="showLoadingRow" class="windowed-grid-loading-row">
      <slot name="loading" :columns="columns" />
    </div>
    <div v-if="after" aria-hidden="true" class="windowed-grid-spacer" :style="{ height: `${after}px` }" />
  </div>
</template>

<style scoped>
.windowed-grid-spacer { grid-column: 1 / -1; pointer-events: none; }
.windowed-grid-cell { min-width: 0; }
.windowed-grid-cell > :deep(*) { width: 100%; }
.windowed-grid-loading-row { grid-column: 1 / -1; min-width: 0; }
</style>
