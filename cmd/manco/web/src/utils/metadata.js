// 不同内容源会用不同分隔符把多个作者拼成一个字符串，
// 这里统一拆成独立作者，保证每个作者各自渲染一个胶囊。
export function splitAuthors(value) {
  const text = String(value || '').trim()
  if (!text) return []
  return text
    .split(/\s*[/、,，;；|&]\s*/)
    .map((part) => part.trim())
    .filter(Boolean)
}
