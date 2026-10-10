<script setup>
import { computed, ref, watch } from 'vue'
import { Check, ChevronRight, Folder, FolderOpen, FolderPlus, HardDrive, Loader2, X } from 'lucide-vue-next'
import { api } from '../api'
import { notify } from '../stores/notices'

const props = defineProps({
  modelValue: { type: String, default: '' },
  placeholder: { type: String, default: '留空使用默认目录' },
  suffix: { type: String, default: '' },
})

const emit = defineEmits(['update:modelValue', 'change'])

const opened = ref(false)
const loading = ref(false)
const creating = ref(false)
const draft = ref(props.modelValue || '')
const roots = ref([])
const current = ref('')
const parent = ref('')
const children = ref([])
const breadcrumbs = ref([])
const defaultDir = ref('')
const folderName = ref('')
const showCreate = ref(false)
const error = ref('')

const previewBase = computed(() => current.value || draft.value.trim() || defaultDir.value || '默认下载目录')
const previewSuffix = computed(() => String(props.suffix || '').replace(/^[/\\]+|[/\\]+$/g, ''))
const previewPath = computed(() => (previewSuffix.value ? `${previewBase.value}/${previewSuffix.value}` : previewBase.value))

watch(
  () => props.modelValue,
  (value) => {
    draft.value = value || ''
  },
)

function commit(value = draft.value) {
  const next = String(value || '').trim()
  draft.value = next
  emit('update:modelValue', next)
  emit('change', next)
}

async function loadRoots() {
  loading.value = true
  error.value = ''
  try {
    const payload = await api.downloadDirectories()
    roots.value = (payload.items || []).filter((item) => item.available || item.default)
    defaultDir.value = payload.default || ''
    current.value = ''
    parent.value = ''
    children.value = []
    breadcrumbs.value = []
    const preferred = draft.value.trim()
    if (preferred) {
      try {
        await loadDirectory(preferred)
      } catch {
        const fallback = defaultDir.value || roots.value.find((item) => item.default)?.path || roots.value[0]?.path
        if (fallback) await loadDirectory(fallback)
      }
    }
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

async function loadDirectory(path) {
  if (!path) return
  loading.value = true
  error.value = ''
  try {
    const payload = await api.downloadDirectories(path)
    current.value = payload.current || path
    parent.value = payload.parent || ''
    children.value = payload.children || []
    breadcrumbs.value = payload.breadcrumbs || []
    roots.value = payload.items || roots.value
    defaultDir.value = payload.default || defaultDir.value
  } catch (err) {
    error.value = err.message
    throw err
  } finally {
    loading.value = false
  }
}

async function openPicker() {
  opened.value = true
  showCreate.value = false
  folderName.value = ''
  await loadRoots()
}

function closePicker() {
  opened.value = false
  showCreate.value = false
}

function selectBase(path) {
  commit(path)
  closePicker()
}

function selectDefault() {
  commit('')
  closePicker()
}

async function enter(path) {
  if (!path) return
  await loadDirectory(path)
}

async function goParent() {
  if (parent.value) await loadDirectory(parent.value)
}

async function createFolder() {
  const name = folderName.value.trim()
  if (!name) {
    notify('请输入文件夹名称', 'warning')
    return
  }
  creating.value = true
  try {
    const payload = await api.createDownloadDirectory(current.value || defaultDir.value, name)
    folderName.value = ''
    showCreate.value = false
    notify(`已创建文件夹 ${name}`, 'success')
    await loadDirectory(payload.path)
  } catch (err) {
    notify(`新建文件夹失败：${err.message}`, 'error')
  } finally {
    creating.value = false
  }
}

function chooseCurrent() {
  selectBase(current.value || defaultDir.value)
}
</script>

<template>
  <div class="directory-picker-wrap">
    <div class="directory-picker-control">
      <input
        v-model="draft"
        class="input"
        :placeholder="placeholder"
        spellcheck="false"
        @keyup.enter="commit()"
        @blur="commit()"
      />
      <button
        class="directory-picker-button"
        type="button"
        aria-label="选择容器内目录"
        title="选择容器内目录"
        @click="openPicker"
      >
        <FolderOpen :size="18" />
      </button>
    </div>
    <p v-if="previewSuffix" class="directory-path-preview" :title="previewPath">
      <span>{{ previewBase }}</span><span class="directory-path-suffix">/{{ previewSuffix }}</span>
    </p>

    <Teleport to="body">
      <div v-if="opened" class="directory-modal-backdrop" @click.self="closePicker">
        <section class="directory-modal" role="dialog" aria-modal="true" aria-label="选择容器内目录">
          <header class="directory-modal-head">
            <div>
              <h2>选择下载位置</h2>
              <p>选择基础目录，作品目录会自动补在当前位置。</p>
            </div>
            <div class="directory-head-actions">
              <button class="btn secondary small" type="button" :disabled="creating" @click="showCreate = !showCreate">
                <FolderPlus :size="15" />
                新建文件夹
              </button>
              <button class="btn ghost icon" type="button" aria-label="关闭" @click="closePicker">
                <X :size="18" />
              </button>
            </div>
          </header>

          <div v-if="showCreate" class="directory-create-row">
            <input
              v-model="folderName"
              class="input"
              placeholder="输入文件夹名称"
              maxlength="120"
              @keyup.enter="createFolder"
            />
            <button class="btn small" type="button" :disabled="creating" @click="createFolder">
              <Loader2 v-if="creating" :size="14" class="spin" />
              <FolderPlus v-else :size="14" />
              创建
            </button>
          </div>

          <div class="directory-modal-toolbar">
            <button class="btn secondary small" type="button" :disabled="!parent || loading" @click="goParent">
              返回上级
            </button>
            <button class="btn secondary small" type="button" :disabled="loading" @click="loadRoots">
              挂载目录
            </button>
            <nav class="directory-breadcrumbs" aria-label="目录路径">
              <template v-for="(crumb, index) in breadcrumbs" :key="crumb.path">
                <ChevronRight v-if="index" :size="14" />
                <button type="button" @click="enter(crumb.path)">{{ crumb.name }}</button>
              </template>
            </nav>
          </div>

          <div class="directory-modal-body">
            <div v-if="loading" class="directory-modal-empty">
              <Loader2 :size="20" class="spin" />
              <span>正在读取目录...</span>
            </div>
            <template v-else-if="current">
              <button v-if="parent" class="directory-entry" type="button" @click="goParent">
                <Folder :size="18" />
                <span>返回上级</span>
              </button>
              <button
                v-for="entry in children"
                :key="entry.path"
                class="directory-entry"
                type="button"
                @click="enter(entry.path)"
              >
                <Folder :size="18" />
                <span class="directory-entry-name">{{ entry.name }}</span>
                <span v-if="!entry.writable" class="badge warning">只读</span>
                <ChevronRight :size="15" />
              </button>
              <div v-if="!children.length" class="directory-modal-empty compact">
                <Folder :size="20" />
                <span>当前目录没有子文件夹</span>
              </div>
            </template>
            <template v-else>
              <button class="directory-entry" type="button" @click="selectDefault">
                <HardDrive :size="18" />
                <span class="directory-entry-name">使用默认下载目录</span>
                <Check v-if="!modelValue" :size="16" />
              </button>
              <button
                v-for="root in roots"
                :key="root.path"
                class="directory-entry"
                type="button"
                @click="enter(root.path)"
              >
                <HardDrive :size="18" />
                <span class="directory-entry-name">{{ root.path }}</span>
                <span v-if="root.default" class="badge">默认</span>
                <ChevronRight :size="15" />
              </button>
            </template>
          </div>

          <footer class="directory-modal-foot">
            <div class="directory-preview" :title="previewPath">
              <span>实际位置：</span>
              <strong>{{ previewBase }}</strong><em>/{{ previewSuffix || '' }}</em>
            </div>
            <div class="inline">
              <button class="btn secondary" type="button" @click="selectDefault">使用默认目录</button>
              <button class="btn" type="button" :disabled="loading || !current" @click="chooseCurrent">
                <Check :size="15" />
                选择此目录
              </button>
            </div>
          </footer>

          <p v-if="error" class="directory-modal-error">{{ error }}</p>
        </section>
      </div>
    </Teleport>
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

.directory-picker-button:hover {
  color: var(--primary-strong);
  background: var(--primary-soft);
}

.directory-path-preview {
  display: block;
  margin: 6px 2px 0;
  overflow: hidden;
  color: var(--text-muted);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.directory-path-suffix {
  color: var(--primary);
  font-weight: 650;
}

.directory-modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 360;
  display: grid;
  place-items: center;
  padding: 24px;
  background: rgba(20, 24, 30, 0.48);
  backdrop-filter: blur(3px);
}

.directory-modal {
  display: flex;
  flex-direction: column;
  width: min(760px, 100%);
  max-height: min(720px, calc(100dvh - 48px));
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface);
  box-shadow: 0 24px 70px rgba(15, 23, 42, 0.25);
}

.directory-modal-head,
.directory-modal-toolbar,
.directory-modal-foot {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 16px;
}

.directory-modal-head {
  justify-content: space-between;
  border-bottom: 1px solid var(--border);
}

.directory-modal-head h2 {
  font-size: 17px;
}

.directory-modal-head p {
  margin: 3px 0 0;
  color: var(--text-muted);
  font-size: 12px;
}

.directory-head-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.directory-create-row {
  display: flex;
  gap: 8px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--border);
  background: var(--surface-muted);
}

.directory-create-row .input {
  flex: 1;
}

.directory-modal-toolbar {
  flex-wrap: wrap;
  border-bottom: 1px solid var(--border);
  background: var(--surface-muted);
}

.directory-breadcrumbs {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  overflow-x: auto;
  color: var(--text-muted);
  font-size: 12px;
  scrollbar-width: none;
}

.directory-breadcrumbs::-webkit-scrollbar {
  display: none;
}

.directory-breadcrumbs button {
  max-width: 150px;
  padding: 3px 4px;
  overflow: hidden;
  border: 0;
  background: transparent;
  color: inherit;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
}

.directory-breadcrumbs button:hover {
  color: var(--primary);
}

.directory-modal-body {
  display: flex;
  flex: 1;
  min-height: 250px;
  flex-direction: column;
  gap: 3px;
  padding: 10px;
  overflow-y: auto;
  scrollbar-width: thin;
  scrollbar-color: color-mix(in srgb, var(--primary) 20%, transparent) transparent;
}

.directory-modal-body::-webkit-scrollbar {
  width: 4px;
}

.directory-modal-body::-webkit-scrollbar-thumb {
  border-radius: 999px;
  background: color-mix(in srgb, var(--primary) 20%, transparent);
}

.directory-entry {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  min-height: 44px;
  padding: 9px 10px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text);
  text-align: left;
  cursor: pointer;
}

.directory-entry:hover {
  color: var(--primary-strong);
  background: var(--primary-soft);
}

.directory-entry-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.directory-modal-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 200px;
  color: var(--text-muted);
}

.directory-modal-empty.compact {
  min-height: 130px;
}

.directory-modal-foot {
  justify-content: space-between;
  flex-wrap: wrap;
  border-top: 1px solid var(--border);
}

.directory-preview {
  min-width: 0;
  overflow: hidden;
  color: var(--text-muted);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.directory-preview strong {
  color: var(--text);
  font-weight: 550;
}

.directory-preview em {
  color: var(--primary);
  font-style: normal;
  font-weight: 700;
}

.directory-modal-error {
  margin: 0;
  padding: 9px 16px;
  color: var(--danger);
  background: var(--danger-soft);
  font-size: 12px;
}

@media (max-width: 640px) {
  .directory-modal-backdrop {
    padding: 10px;
  }

  .directory-modal {
    max-height: calc(100dvh - 20px);
  }

  .directory-modal-head,
  .directory-modal-toolbar,
  .directory-modal-foot {
    align-items: stretch;
    flex-direction: column;
  }

  .directory-head-actions,
  .directory-modal-foot .inline {
    justify-content: flex-end;
  }
}
</style>
