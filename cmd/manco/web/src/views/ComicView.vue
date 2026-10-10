<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  BookOpen,
  CalendarClock,
  ChevronLeft,
  Download,
  FolderOpen,
  Languages,
  Loader2,
  Rss,
  Square,
  SquareCheck,
  X,
} from 'lucide-vue-next'
import { api } from '../api'
import DirectoryPicker from '../components/DirectoryPicker.vue'

const route = useRoute()
const router = useRouter()
const sourceId = route.params.sourceId
const comicId = route.params.comicId
const isBook = ref(false)

const detail = ref(null)
const selected = ref(new Set())
const loading = ref(true)
const busy = ref(false)
const message = ref('')
const error = ref('')
const autoDownload = ref(true)

const subscribeOpen = ref(false)
const subForm = ref({
  cronExpr: '',
  downloadDir: '',
  convertToSimplified: false,
})

const comic = computed(() => detail.value?.comic || null)
const chapters = computed(() => detail.value?.chapters || [])
const allSelected = computed(() => chapters.value.length > 0 && selected.value.size === chapters.value.length)

function defaultCron() {
  const now = new Date()
  return `${now.getMinutes()} ${now.getHours()} * * ${now.getDay()}`
}

onMounted(async () => {
  try {
    detail.value = await api.comic(sourceId, comicId)
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
  try {
    const payload = await api.sources()
    const source = (payload.items || []).find((item) => item.id === sourceId)
    isBook.value = source?.kind === 'book'
  } catch {
    isBook.value = false
  }
})


function goBack() {
  if (window.history.state?.back) {
    router.back()
  } else {
    router.push({ name: 'discover' })
  }
}

function toggle(id) {
  const next = new Set(selected.value)
  if (next.has(id)) {
    next.delete(id)
  } else {
    next.add(id)
  }
  selected.value = next
}

function toggleAll() {
  selected.value = allSelected.value ? new Set() : new Set(chapters.value.map((item) => item.id))
}

function cover() {
  return api.imageUrl(comic.value?.cover, sourceId)
}

async function openSubscribe() {
  message.value = ''
  error.value = ''
  subForm.value = {
    cronExpr: subForm.value.cronExpr || defaultCron(),
    downloadDir: subForm.value.downloadDir,
    convertToSimplified: Boolean(subForm.value.convertToSimplified),
  }
  subscribeOpen.value = true
}

async function confirmSubscribe() {
  if (!comic.value) return
  if (!subForm.value.cronExpr.trim()) {
    error.value = 'Cron 表达式不能为空'
    return
  }
  busy.value = true
  error.value = ''
  message.value = ''
  try {
    const last = chapters.value[chapters.value.length - 1]
    await api.createSubscription({
      sourceId,
      comicId: comic.value.id,
      title: comic.value.title,
      cover: comic.value.cover,
      author: comic.value.author,
      autoDownload: autoDownload.value,
      enabled: true,
      baseline: last?.id || '',
      lastChapterOrder: last?.order || 0,
      cronExpr: subForm.value.cronExpr.trim(),
      downloadDir: subForm.value.downloadDir.trim(),
      convertToSimplified: Boolean(subForm.value.convertToSimplified),
    })
    subscribeOpen.value = false
    message.value = '订阅已创建，新章节会按检查计划自动进入下载队列。'
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function downloadSelected() {
  const picked = chapters.value.filter((item) => selected.value.has(item.id))
  if (!picked.length || !comic.value) return
  busy.value = true
  error.value = ''
  message.value = ''
  try {
    const result = await api.createDownload({
      sourceId,
      comicId: comic.value.id,
      comicTitle: comic.value.title,
      comicCover: comic.value.cover,
      autoDownload: autoDownload.value,
      chapters: picked,
      downloadDir: subForm.value.downloadDir.trim(),
      convertToSimplified: Boolean(subForm.value.convertToSimplified),
    })
    const queued = Number(result.queued || 0)
    const skipped = Number(result.skipped || 0)
    message.value = [`已加入 ${queued} 个章节`, skipped ? `跳过 ${skipped} 个已下载章节` : '']
      .filter(Boolean)
      .join('，') + '。'
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div v-if="loading" class="empty">
    <Loader2 :size="22" class="spin" />
    <span>加载作品信息</span>
  </div>
  <div v-else-if="!comic" class="card empty">
    <BookOpen :size="26" />
    <p>{{ error || '没有找到该作品' }}</p>
  </div>
  <div v-else>
    <div class="detail-back">
      <button class="btn secondary small" type="button" @click="goBack">
        <ChevronLeft :size="15" />
        返回
      </button>
    </div>
    <div v-if="error" class="alert error">{{ error }}</div>
    <div v-if="message" class="alert ok">{{ message }}</div>

    <div class="detail-hero">
      <div class="detail-cover">
        <img v-if="comic.cover" :src="cover()" :alt="comic.title" />
      </div>
      <div class="detail-info">
        <h2>{{ comic.title }}</h2>
        <div class="meta">
          <span>{{ comic.author || '未知作者' }}</span>
          <span v-if="comic.status"> · {{ comic.status }}</span>
          <span> · 共 {{ chapters.length }} 话</span>
        </div>
        <div v-if="comic.tags?.length" class="tag-list">
          <span v-for="tag in comic.tags" :key="tag" class="badge">{{ tag }}</span>
        </div>
        <p class="description">{{ comic.description || '暂无简介' }}</p>
      </div>
    </div>

    <div class="two-col">
      <section>
        <div class="section-head">
          <h2>章节</h2>
          <button class="btn ghost small" type="button" @click="toggleAll">
            <component :is="allSelected ? SquareCheck : Square" :size="15" />
            {{ allSelected ? '取消全选' : '全选' }}
          </button>
        </div>
        <div class="chapter-list">
          <button
            v-for="chapter in chapters"
            :key="chapter.id"
            class="chapter-row"
            type="button"
            style="border: none; width: 100%; text-align: left; cursor: pointer"
            @click="toggle(chapter.id)"
          >
            <component :is="selected.has(chapter.id) ? SquareCheck : Square" :size="16" />
            <span class="title">{{ chapter.title }}</span>
            <span v-if="chapter.order" class="badge">#{{ chapter.order }}</span>
          </button>
          <div v-if="!chapters.length" class="empty">该作品还没有可选章节</div>
        </div>
      </section>

      <aside class="card card-pad">
        <h2 style="font-size: 16px; margin-bottom: 12px">操作</h2>
        <div class="field" style="margin-bottom: 12px">
          <label class="inline">
            <input v-model="autoDownload" type="checkbox" />
            <span>加入后自动追更订阅</span>
          </label>
        </div>
        <div class="inline" style="flex-direction: column; align-items: stretch; gap: 8px">
          <button class="btn" type="button" :disabled="busy || !selected.size" @click="downloadSelected">
            <Loader2 v-if="busy" :size="16" class="spin" />
            <Download v-else :size="16" />
            下载所选章节（{{ selected.size }}）
          </button>
          <button class="btn secondary" type="button" :disabled="busy" @click="openSubscribe">
            <Rss :size="16" />
            订阅追更
          </button>
        </div>
        <p class="muted small" style="margin-bottom: 0">
          {{
            isBook
              ? '每个章节会单独生成一个文本文件，并写入 book.json 元数据。'
              : '每个章节会单独生成一个 CBZ 压缩包，文件夹结构为作品名/章节名.cbz。'
          }}
        </p>
      </aside>
    </div>
  </div>

  <div v-if="subscribeOpen" class="modal-backdrop" @click.self="subscribeOpen = false">
    <div class="modal">
      <div class="modal-head">
        <div>
          <h2>订阅任务设置</h2>
          <p class="muted small">{{ comic?.title }}</p>
        </div>
        <button class="btn ghost icon" type="button" aria-label="关闭" @click="subscribeOpen = false">
          <X :size="18" />
        </button>
      </div>
      <div class="modal-body">
        <label class="field">
          <span class="inline"><CalendarClock :size="14" /> Cron 检查周期</span>
          <input v-model="subForm.cronExpr" class="input" placeholder="0 21 * * 5" />
          <span class="muted small">默认按当前时间的整点生成每周检查周期，可改为任意 Cron 表达式。</span>
        </label>
        <label class="field">
          <span class="inline"><FolderOpen :size="14" /> 下载位置</span>
          <DirectoryPicker v-model="subForm.downloadDir" />

        </label>
        <label class="switch-row">
          <input v-model="subForm.convertToSimplified" type="checkbox" />
          <span><Languages :size="14" /> 下载时执行繁体转简体（不影响完成/失败记录）</span>
        </label>
      </div>
      <div class="modal-foot">
        <button class="btn secondary" type="button" @click="subscribeOpen = false">取消</button>
        <button class="btn" type="button" :disabled="busy" @click="confirmSubscribe">
          <Loader2 v-if="busy" :size="15" class="spin" />
          <Rss v-else :size="15" />
          创建订阅
        </button>
      </div>
    </div>
  </div>
</template>
