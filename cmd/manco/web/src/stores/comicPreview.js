// 从探索发现点进作品详情时，先把列表里已有的卡片数据交给详情页渲染首屏，
// 详情请求回来后再覆盖成完整数据，避免每次进详情都白屏等一轮网络。
const previews = new Map()
const MAX_PREVIEWS = 200

export function rememberComicPreview(item) {
  if (!item?.sourceId || !item?.id) return
  previews.set(`${item.sourceId}:${item.id}`, {
    id: item.id,
    sourceId: item.sourceId,
    title: item.title,
    cover: item.cover,
    author: item.author,
    description: item.description,
    status: item.status,
    tags: Array.isArray(item.tags) ? item.tags : [],
    lastChapterTitle: item.lastChapterTitle,
  })
  if (previews.size > MAX_PREVIEWS) {
    for (const key of previews.keys()) {
      previews.delete(key)
      if (previews.size <= MAX_PREVIEWS) break
    }
  }
}

export function comicPreview(sourceId, comicId) {
  return previews.get(`${sourceId}:${comicId}`) || null
}
