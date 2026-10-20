import assert from 'node:assert/strict'
import test from 'node:test'
import { refreshTaskRows } from './taskPages.js'

test('polling removes completed/deleted rows from the first page and visible tail', () => {
  const before = [1, 2, 3, 4, 5, 6].map((id) => ({ id, status: 'queued' }))
  const first = [{ id: 2, status: 'running' }, { id: 3, status: 'queued' }]
  const result = refreshTaskRows(before, first, new Set([1, 2]), [5, 6], [{ id: 6, status: 'failed' }])
  assert.deepEqual(result.map((item) => item.id), [2, 3, 4, 6])
  assert.equal(result.at(-1).status, 'failed')
})

test('priority jobs move to the front without duplicating loaded rows', () => {
  const before = [1, 2, 3, 4].map((id) => ({ id }))
  const result = refreshTaskRows(before, [{ id: 4 }, { id: 1 }], new Set([1, 2]))
  assert.deepEqual(result.map((item) => item.id), [4, 1, 3])
})
