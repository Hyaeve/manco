<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { Check, ChevronDown } from 'lucide-vue-next'

const props = defineProps({
  modelValue: { type: String, default: '' },
  options: { type: Array, default: () => [] },
  placeholder: { type: String, default: 'https://example.com' },
})
const emit = defineEmits(['update:modelValue', 'change'])

const root = ref(null)
const trigger = ref(null)
const opened = ref(false)
const draft = ref(props.modelValue || '')
const selected = computed(() => props.options.includes(props.modelValue))

function sync(value) {
  draft.value = value || ''
  emit('update:modelValue', value || '')
  emit('change', value || '')
}

function choose(value) {
  sync(value)
  opened.value = false
}

async function show() {
  opened.value = true
  await nextTick()
  trigger.value?.focus()
}

function outside(event) {
  if (!root.value?.contains(event.target)) {
    opened.value = false
    if (draft.value !== props.modelValue) sync(draft.value.trim())
  }
}

onMounted(() => {
  document.addEventListener('pointerdown', outside)
})

onUnmounted(() => document.removeEventListener('pointerdown', outside))
</script>

<template>
  <div ref="root" class="site-picker" :class="{ open: opened }">
    <div class="site-picker-control">
      <input
        ref="trigger"
        v-model="draft"
        class="input"
        :placeholder="placeholder"
        spellcheck="false"
        @focus="show"
        @keyup.enter="sync(draft.trim()); opened = false"
        @blur="sync(draft.trim())"
      />
      <button class="site-picker-toggle" type="button" aria-label="选择内置站点" :aria-expanded="opened" @click="opened ? (opened = false) : show()">
        <ChevronDown :size="16" :class="{ expanded: opened }" />
      </button>
    </div>
    <Transition name="select-popup">
      <div v-if="opened && options.length" class="site-picker-popup">
        <button
          v-for="option in options"
          :key="option"
          class="site-picker-option"
          type="button"
          @mousedown.prevent="choose(option)"
        >
          <span>{{ option }}</span>
          <Check v-if="option === modelValue" :size="15" />
        </button>
        <p v-if="!selected && draft" class="muted small">回车保存自定义地址</p>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.site-picker {
  position: relative;
}

.site-picker-control {
  position: relative;
}

.site-picker-control .input {
  padding-right: 38px;
}

.site-picker-toggle {
  position: absolute;
  top: 50%;
  right: 5px;
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  padding: 0;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--text-muted);
  transform: translateY(-50%);
}

.site-picker-toggle:hover {
  color: var(--primary);
  background: var(--primary-soft);
}

.site-picker-toggle .expanded {
  transform: rotate(180deg);
}

.site-picker-popup {
  position: absolute;
  top: calc(100% + 6px);
  right: 0;
  left: 0;
  z-index: 120;
  max-height: 260px;
  padding: 5px;
  overflow-y: auto;
  border: 1px solid var(--border);
  border-radius: 7px;
  background: var(--surface);
  box-shadow: var(--shadow);
}

.site-picker-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  width: 100%;
  min-height: 36px;
  padding: 8px 10px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--text);
  text-align: left;
}

.site-picker-option:hover {
  color: var(--primary-strong);
  background: var(--primary-soft);
}

.site-picker-option span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.site-picker-popup p {
  margin: 6px 8px 5px;
}
</style>
