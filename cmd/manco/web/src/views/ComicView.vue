<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { BookOpen, Download, Loader2, Rss, Square, SquareCheck } from 'lucide-vue-next'
import { api } from '../api'

const route = useRoute()
const sourceId = route.params.sourceId
const comicId = route.params.comicId

const detail = ref(null)
const selected = ref(new Set())
const loading = ref(true)
const busy = ref(false)
const message = ref('')
const error = ref('')
const autoDownload = ref(true)

const comic = computed(() => detail.value?.comic || null)
const chapters = computed(() => detail.value?.chapters || [])
const allSelected = computed(() => chapters.value.length > 0 && selected.value.size === chapters.value.length)

onMounted(async () => {
  try {
    detail.value = await api.comic(sourceId, comicId)
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
})

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

async function subscribe() {
  if (!comic.value) return
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
    })
    message.value = '订阅成功，新章节会自动进入下载队列。'
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
          <button class="btn secondary" type="button" :disabled="busy" @click="subscribe">
            <Rss :size="16" />
            订阅追更
          </button>
        </div>
        <p class="muted small" style="margin-bottom: 0">
          每个章节会单独生成一个 CBZ 压缩包，文件夹结构为作品名/章节名.cbz。
        </p>
      </aside>
    </div>
  </div>
</template>
