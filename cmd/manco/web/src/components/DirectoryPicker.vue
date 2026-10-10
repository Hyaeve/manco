<script setup>
import { computed, onMounted, ref } from 'vue'
import { Check, ChevronDown, FolderOpen, HardDrive } from 'lucide-vue-next'
import { api } from '../api'

const props = defineProps({
  modelValue: { type: String, default: '' },
  placeholder: { type: String, default: '选择容器内目录' },
})
const emit = defineEmits(['update:modelValue', 'change'])

const options = ref([])
const opened = ref(false)
const loading = ref(false)
const manual = ref(props.modelValue || '')

const selectedLabel = computed(() => {
  const match = options.value.find((item) => item.path === props.modelValue)
  return match?.path || props.modelValue || props.placeholder
})

async function load() {
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

function choose(path) {
  manual.value = path
  emit('update:modelValue', path)
  emit('change', path)
  opened.value = false
}

function applyManual() {
  const value = manual.value.trim()
  emit('update:modelValue', value)
  emit('change', value)
}

onMounted(load)
</script>

<template>
  <div class="directory-picker-wrap">
    <button class="directory-picker-trigger" type="button" :aria-expanded="opened" @click="opened = !opened">
      <span class="inline">
        <HardDrive :size="16" />
        <span>{{ selectedLabel }}</span>
      </span>
      <ChevronDown :size="16" :class="{ expanded: opened }" />
    </button>
    <div v-if="opened" class="directory-picker-popup">
      <p v-if="loading" class="muted small">正在读取容器目录...</p>
      <div v-else class="directory-options">
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
      <div class="directory-picker-manual">
        <input
          v-model="manual"
          class="input"
          placeholder="/mnt/downloads"
          @blur="applyManual"
          @keyup.enter="applyManual"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.directory-picker-wrap {
  position: relative;
}

.directory-picker-trigger {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  width: 100%;
  min-height: 40px;
  padding: 8px 11px;
  border: 1px solid var(--border-strong);
  border-radius: 6px;
  background: var(--surface);
  color: var(--text);
  text-align: left;
}

.directory-picker-trigger > span {
  min-width: 0;
}

.directory-picker-trigger span span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.directory-picker-popup {
  position: absolute;
  top: calc(100% + 6px);
  right: 0;
  left: 0;
  z-index: 120;
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: 7px;
  background: var(--surface);
  box-shadow: var(--shadow);
}

.directory-picker-popup .directory-options {
  max-height: 220px;
  margin: 0 0 8px;
}

.directory-option-path {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.directory-picker-manual .input {
  min-height: 36px;
}

.expanded {
  transform: rotate(180deg);
}
</style>
