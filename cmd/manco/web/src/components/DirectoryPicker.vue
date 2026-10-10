<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { Check, FolderOpen, HardDrive, Loader2 } from 'lucide-vue-next'
import { api } from '../api'

const props = defineProps({
  modelValue: { type: String, default: '' },
  placeholder: { type: String, default: '留空使用默认目录' },
})
const emit = defineEmits(['update:modelValue', 'change'])

const root = ref(null)
const options = ref([])
const opened = ref(false)
const loading = ref(false)
const draft = ref(props.modelValue || '')

const selectedLabel = computed(() => {
  const match = options.value.find((item) => item.path === props.modelValue)
  return match?.path || props.modelValue || props.placeholder
})

watch(
  () => props.modelValue,
  (value) => {
    draft.value = value || ''
  },
)

async function load() {
  if (options.value.length || loading.value) return
  loading.value = true
  try {
    const payload = await api.downloadDirectories()
    options.value = (payload.items || []).filter((item) => item.available || item.default)
  } catch {
    options.value = []
  } finally {
    loading.value = false
  }
}

function commit(value = draft.value) {
  const next = String(value || '').trim()
  draft.value = next
  emit('update:modelValue', next)
  emit('change', next)
}

function choose(path) {
  commit(path)
  opened.value = false
}

function toggle() {
  opened.value = !opened.value
  if (opened.value) load()
}

function outside(event) {
  if (root.value?.contains(event.target)) return
  if (opened.value) commit()
  opened.value = false
}

onMounted(() => document.addEventListener('pointerdown', outside))
onUnmounted(() => document.removeEventListener('pointerdown', outside))
</script>

<template>
  <div ref="root" class="directory-picker-wrap">
    <div class="directory-picker-control">
      <input
        v-model="draft"
        class="input"
        :placeholder="placeholder"
        spellcheck="false"
        @keyup.enter="commit()"
      />
      <button
        class="directory-picker-button"
        type="button"
        aria-label="选择容器内目录"
        :aria-expanded="opened"
        @click="toggle"
      >
        <FolderOpen :size="18" />
      </button>
    </div>

    <Transition name="select-popup">
      <div v-if="opened" class="directory-picker-popup">
        <header class="directory-picker-head">
          <span>选择容器内目录</span>
          <small>{{ selectedLabel }}</small>
        </header>
        <div v-if="loading" class="directory-picker-loading">
          <Loader2 :size="17" class="spin" />
          <span>正在读取目录...</span>
        </div>
        <div v-else class="directory-options">
          <button class="directory-option" type="button" @click="choose('')">
            <HardDrive :size="16" />
            <span class="directory-option-path">使用默认下载目录</span>
            <Check v-if="!modelValue" :size="15" />
          </button>
          <button
            v-for="item in options"
            :key="item.path"
            class="directory-option"
            type="button"
            @click="choose(item.path)"
          >
            <FolderOpen :size="16" />
            <span class="directory-option-path">{{ item.path }}</span>
            <span v-if="item.default" class="badge">默认</span>
            <Check v-if="item.path === modelValue" :size="15" />
          </button>
        </div>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.directory-picker-wrap {
  position: relative;
}

.directory-picker-control {
  position: relative;
}

.directory-picker-control .input {
  min-height: 40px;
  padding-right: 44px;
}

.directory-picker-button {
  position: absolute;
  top: 50%;
  right: 5px;
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  transform: translateY(-50%);
}

.directory-picker-button:hover,
.directory-picker-button[aria-expanded='true'] {
  color: var(--primary);
  background: var(--primary-soft);
}

.directory-picker-popup {
  position: absolute;
  top: calc(100% + 7px);
  right: 0;
  left: 0;
  z-index: 160;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  box-shadow: var(--shadow);
}

.directory-picker-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
  background: var(--surface-muted);
}

.directory-picker-head small {
  min-width: 0;
  overflow: hidden;
  color: var(--text-muted);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.directory-picker-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 86px;
  color: var(--text-muted);
}

.directory-picker-popup .directory-options {
  max-height: 250px;
  padding: 5px;
  overflow-y: auto;
}

.directory-option {
  display: flex;
  align-items: center;
  gap: 9px;
  width: 100%;
  min-height: 38px;
  padding: 8px 9px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text);
  text-align: left;
  cursor: pointer;
}

.directory-option:hover {
  color: var(--primary-strong);
  background: var(--primary-soft);
}

.directory-option-path {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
