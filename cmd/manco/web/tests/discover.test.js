import assert from 'node:assert/strict'
import { test } from 'node:test'

const NAV_KEY = 'manco.discover.nav.v1'

async function createSession(saved = null, failStorage = false) {
  const storage = new Map(saved === null ? [] : [[NAV_KEY, saved]])
  globalThis.window = {
    sessionStorage: {
      getItem: (key) => {
        if (failStorage) throw new Error('storage unavailable')
        return storage.get(key) ?? null
      },
      setItem: (key, value) => {
        if (failStorage) throw new Error('storage unavailable')
        storage.set(key, value)
      },
      removeItem: (key) => {
        if (failStorage) throw new Error('storage unavailable')
        storage.delete(key)
      },
    },
  }
  return import(`../src/stores/discover.js?session=${Math.random()}`)
}

test('new browser sessions default to Bangumi recent', async () => {
  const session = await createSession()
  assert.deepEqual(session.readDiscoverNav(), {
    kind: 'bangumi', source: 'bangumi-recent', page: 1, scrollTop: 0,
  })
})

test('same-session navigation and page reload retain the selected source', async () => {
  const session = await createSession()
  const expected = {
    kind: 'book', source: 'zlibrary', page: 3, scrollTop: 240,
    sourceByKind: { book: 'zlibrary', bangumi: 'bangumi-novel' },
  }
  session.saveDiscoverNav(expected)
  assert.deepEqual(session.readDiscoverNav(), expected)
  const saved = window.sessionStorage.getItem(NAV_KEY)
  const reloaded = await createSession(saved)
  assert.deepEqual(reloaded.readDiscoverNav(), expected)
})

test('new login clears stale navigation and panels after a container restart', async () => {
  const session = await createSession(JSON.stringify({ kind: 'comic', source: 'baozimh', page: 7 }))
  session.readDiscoverPanels().baozimh = { loaded: true, page: 7 }
  session.markDiscoverPanelsLoaded()
  assert.equal(session.discoverPanelsExpired(), false)
  session.resetDiscoverSession()
  assert.equal(window.sessionStorage.getItem(NAV_KEY), null)
  assert.deepEqual(session.readDiscoverPanels(), {})
  assert.equal(session.discoverPanelsExpired(), true)
  assert.equal(session.readDiscoverNav().kind, 'bangumi')
  assert.equal(session.readDiscoverNav().source, 'bangumi-recent')
})

test('missing or unavailable session storage still defaults to Bangumi', async () => {
  for (const saved of ['broken json', 'null', '[]']) {
    const session = await createSession(saved)
    assert.equal(session.readDiscoverNav().source, 'bangumi-recent')
  }
  const session = await createSession(null, true)
  session.resetDiscoverSession()
  assert.equal(session.readDiscoverNav().kind, 'bangumi')
  session.saveDiscoverNav({ kind: 'comic', source: 'baozimh' })
  assert.equal(session.readDiscoverNav().source, 'baozimh')
})
