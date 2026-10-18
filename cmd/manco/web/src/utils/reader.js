const TEXT_PAGE_TARGET = 1800
const TEXT_PAGE_MINIMUM = 1100
const TEXT_PAGE_BREAKS = new Set(['。', '！', '？', '；', '\n'])

export function paginateTextPages(value) {
  const characters = Array.from(String(value || '').replace(/\r/g, '').trim())
  if (!characters.length) return []
  const pages = []
  let offset = 0
  while (offset < characters.length) {
    let end = Math.min(offset + TEXT_PAGE_TARGET, characters.length)
    if (end < characters.length) {
      for (let cursor = end; cursor > offset + TEXT_PAGE_MINIMUM; cursor -= 1) {
        if (TEXT_PAGE_BREAKS.has(characters[cursor - 1])) {
          end = cursor
          break
        }
      }
    }
    const page = characters.slice(offset, end).join('').trim()
    if (page) pages.push(page)
    offset = end
  }
  return pages
}
