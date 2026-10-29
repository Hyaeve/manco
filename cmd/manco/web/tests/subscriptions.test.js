import assert from 'node:assert/strict'
import { test } from 'node:test'
import { api } from '../src/api.js'

test('subscription searches preserve paging, directory, kind and sort options', async (t) => {
  const requests = []
  t.mock.method(globalThis, 'fetch', async (path) => {
    requests.push(new URL(path, 'http://fixture.local'))
    return new Response(JSON.stringify({ items: [], total: 0 }))
  })
  await api.subscriptions(60, 60, {
    query: '星海 100%_&作者',
    kind: 'book',
    dir: '/downloads/小说',
    sort: 'title',
    desc: false,
  })
  const params = requests[0].searchParams
  assert.equal(requests[0].pathname, '/api/subscriptions')
  assert.equal(params.get('q'), '星海 100%_&作者')
  assert.equal(params.get('limit'), '60')
  assert.equal(params.get('offset'), '60')
  assert.equal(params.get('kind'), 'book')
  assert.equal(params.get('dir'), '/downloads/小说')
  assert.equal(params.get('sort'), 'title')
  assert.equal(params.get('desc'), '0')

  await api.subscriptions(60, 0, { query: '', kind: 'all' })
  assert.equal(requests[1].searchParams.has('q'), false)
  assert.equal(requests[1].searchParams.has('offset'), false)
  assert.equal(requests[1].searchParams.has('kind'), false)
})
