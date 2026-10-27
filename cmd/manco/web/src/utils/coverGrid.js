export const COVER_GRID_ROWS_PER_PAGE = 7

const COVER_GRID_GAP = 12
const PREVIOUS_COVER_CARD_MIN_WIDTH = 132

export function coverGridColumnsForWidth(width) {
  const previousColumns = Math.floor(
    (Math.max(0, width) + COVER_GRID_GAP) / (PREVIOUS_COVER_CARD_MIN_WIDTH + COVER_GRID_GAP),
  )
  return Math.max(2, previousColumns - 1)
}

export function estimatedCoverGridColumns(viewportWidth = window.innerWidth) {
  const shellWidth = viewportWidth <= 960 ? viewportWidth : viewportWidth - 248
  return coverGridColumnsForWidth(Math.max(0, shellWidth - 32))
}
