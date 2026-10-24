<script setup>
import { computed, ref, watch } from 'vue'
import { ChevronLeft, ChevronRight } from 'lucide-vue-next'

const props = defineProps({
  page: { type: Number, default: 1 },
  pageSize: { type: Number, default: 30 },
  total: { type: Number, default: 0 },
  disabled: { type: Boolean, default: false },
})
const emit = defineEmits(['change'])
const jumpInput = ref('')

const totalPages = computed(() => Math.max(1, Math.ceil(Math.max(0, props.total) / Math.max(1, props.pageSize))))
const currentPage = computed(() => Math.min(totalPages.value, Math.max(1, props.page)))
const startItem = computed(() => (props.total > 0 ? (currentPage.value - 1) * props.pageSize + 1 : 0))
const endItem = computed(() => Math.min(props.total, currentPage.value * props.pageSize))

const pageItems = computed(() => {
  const total = totalPages.value
  if (total <= 7) {
    return Array.from({ length: total }, (_, index) => ({ key: `page-${index + 1}`, page: index + 1 }))
  }
  const pages = new Set([1, total, currentPage.value])
  for (let offset = -1; offset <= 1; offset += 1) pages.add(currentPage.value + offset)
  if (currentPage.value <= 4) {
    for (let page = 2; page <= 5; page += 1) pages.add(page)
  }
  if (currentPage.value >= total - 3) {
    for (let page = total - 4; page < total; page += 1) pages.add(page)
  }
  const sorted = [...pages].filter((page) => page >= 1 && page <= total).sort((left, right) => left - right)
  const result = []
  for (let index = 0; index < sorted.length; index += 1) {
    const page = sorted[index]
    if (index > 0 && page - sorted[index - 1] > 1) {
      result.push({ key: `gap-${sorted[index - 1]}-${page}`, ellipsis: true })
    }
    result.push({ key: `page-${page}`, page })
  }
  return result
})

function goToPage(value) {
  const page = Math.min(totalPages.value, Math.max(1, Number.parseInt(value, 10) || 1))
  if (!props.disabled && page !== currentPage.value) emit('change', page)
}

function submitJump() {
  goToPage(jumpInput.value)
  jumpInput.value = ''
}

watch(currentPage, () => {
  jumpInput.value = ''
})
</script>

<template>
  <nav class="pagination-bar" aria-label="分页">
    <span class="pagination-summary">
      {{ startItem }}-{{ endItem }} / 共 {{ total }} 项
    </span>
    <div class="pagination-controls">
      <button
        class="pagination-icon"
        type="button"
        :disabled="disabled || currentPage <= 1"
        aria-label="上一页"
        title="上一页"
        @click="goToPage(currentPage - 1)"
      >
        <ChevronLeft :size="17" />
      </button>
      <button
        v-for="item in pageItems"
        :key="item.key"
        class="pagination-page"
        :class="{ active: item.page === currentPage, ellipsis: item.ellipsis }"
        type="button"
        :disabled="disabled || item.ellipsis"
        @click="goToPage(item.page)"
      >
        {{ item.ellipsis ? '…' : item.page }}
      </button>
      <button
        class="pagination-icon"
        type="button"
        :disabled="disabled || currentPage >= totalPages"
        aria-label="下一页"
        title="下一页"
        @click="goToPage(currentPage + 1)"
      >
        <ChevronRight :size="17" />
      </button>
      <form class="pagination-jump" @submit.prevent="submitJump">
        <span>跳至</span>
        <input
          v-model="jumpInput"
          type="number"
          min="1"
          :max="totalPages"
          inputmode="numeric"
          aria-label="跳转页码"
          :disabled="disabled"
        />
        <span>页</span>
        <button class="pagination-jump-button" type="submit" :disabled="disabled || !jumpInput">跳转</button>
      </form>
    </div>
  </nav>
</template>

<style scoped>
.pagination-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  width: 100%;
  margin-top: 18px;
  color: var(--text-muted);
  font-size: 12px;
}

.pagination-summary {
  flex: 0 0 auto;
}

.pagination-controls,
.pagination-jump {
  display: flex;
  align-items: center;
}

.pagination-controls {
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 5px;
}

.pagination-icon,
.pagination-page,
.pagination-jump-button,
.pagination-jump input {
  height: 30px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text-muted);
}

.pagination-icon,
.pagination-page {
  display: inline-grid;
  min-width: 30px;
  place-items: center;
  padding: 0 7px;
  border-radius: 8px;
  cursor: pointer;
}

.pagination-icon:hover:not(:disabled),
.pagination-page:hover:not(:disabled),
.pagination-jump-button:hover:not(:disabled) {
  border-color: color-mix(in srgb, var(--primary) 40%, var(--border));
  background: color-mix(in srgb, var(--primary-soft) 56%, var(--surface));
  color: var(--primary-strong);
}

.pagination-page.active {
  border-color: color-mix(in srgb, var(--primary) 54%, var(--border));
  background: color-mix(in srgb, var(--primary-soft) 78%, var(--surface));
  color: var(--primary-strong);
  font-weight: 700;
}

.pagination-page.ellipsis {
  cursor: default;
}

.pagination-icon:disabled,
.pagination-page:disabled,
.pagination-jump-button:disabled,
.pagination-jump input:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.pagination-jump {
  gap: 5px;
  margin-left: 5px;
  white-space: nowrap;
}

.pagination-jump input {
  width: 54px;
  padding: 0 7px;
  border-radius: 7px;
  outline: none;
  text-align: center;
}

.pagination-jump input:focus {
  border-color: var(--primary);
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--primary-soft) 72%, transparent);
}

.pagination-jump-button {
  padding: 0 9px;
  border-radius: 7px;
  cursor: pointer;
}

@media (max-width: 760px) {
  .pagination-bar {
    align-items: flex-start;
    flex-direction: column;
  }

  .pagination-controls {
    justify-content: flex-start;
  }

  .pagination-summary {
    order: 2;
  }
}
</style>
