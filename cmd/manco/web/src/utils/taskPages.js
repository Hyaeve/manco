// Replace the previous first page, then reconcile only the visible tail.
// Completed/deleted rows missing from a snapshot must not live on forever.
export function refreshTaskRows(previous, firstPage, previousFirstIDs, refreshedIDs = [], refreshed = []) {
  const firstIDs = new Set(firstPage.map((item) => item.id))
  const visibleIDs = new Set(refreshedIDs)
  const updates = new Map(refreshed.map((item) => [item.id, item]))
  const seen = new Set(firstIDs)
  const tail = []
  for (const item of previous) {
    if (previousFirstIDs.has(item.id) || seen.has(item.id)) continue
    if (visibleIDs.has(item.id) && !updates.has(item.id)) continue
    tail.push(updates.get(item.id) || item)
    seen.add(item.id)
  }
  return [...firstPage, ...tail]
}
