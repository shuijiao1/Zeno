import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  DetailMemoryCache,
  detailCacheFreshTtlMs,
  detailCacheKey,
  detailCacheMaxAgeMs,
  detailSessionCacheMaxEntries,
  loadCachedDetailData,
  nodeLatencyCachePrefix,
  rememberDetailData,
} from './detailCache'

class FakeSessionStorage implements Storage {
  private readonly items = new Map<string, string>()
  quotaFailuresRemaining = 0
  alwaysExceedQuota = false

  get length(): number {
    return this.items.size
  }

  clear(): void {
    this.items.clear()
  }

  getItem(key: string): string | null {
    return this.items.get(key) ?? null
  }

  key(index: number): string | null {
    return Array.from(this.items.keys())[index] ?? null
  }

  removeItem(key: string): void {
    this.items.delete(key)
  }

  setItem(key: string, value: string): void {
    if (this.alwaysExceedQuota || this.quotaFailuresRemaining > 0) {
      this.quotaFailuresRemaining = Math.max(0, this.quotaFailuresRemaining - 1)
      const error = new Error('quota exceeded')
      error.name = 'QuotaExceededError'
      throw error
    }
    this.items.set(key, value)
  }
}

const validObject = (value: unknown): { value: string } | null => {
  const candidate = value as { value?: unknown } | null
  return candidate && typeof candidate.value === 'string' ? { value: candidate.value } : null
}

function installStorage(storage: Storage): void {
  vi.stubGlobal('window', { sessionStorage: storage })
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('DetailMemoryCache', () => {
  it('has an explicit LRU capacity and deletes entries as soon as they are read after expiry', () => {
    const cache = new DetailMemoryCache<string>(2, 100)
    cache.set('alpha', 'A', 1_000)
    cache.set('beta', 'B', 1_001)

    expect(cache.get('alpha', 1_050)).toBe('A')
    cache.set('gamma', 'C', 1_002)

    expect(cache.get('beta', 1_050)).toBeUndefined()
    expect(cache.get('alpha', 1_101)).toBeUndefined()
    expect(cache.size).toBe(1)
  })
})

describe('session detail cache', () => {
  it('deletes expired and malformed entries instead of leaving them in sessionStorage', () => {
    const storage = new FakeSessionStorage()
    installStorage(storage)
    const expiredKey = detailCacheKey(nodeLatencyCachePrefix, 'expired', '1h')
    const malformedKey = detailCacheKey(nodeLatencyCachePrefix, 'malformed', '1h')
    const now = 1_000_000
    storage.setItem(expiredKey, JSON.stringify({ storedAt: now - detailCacheMaxAgeMs - 1, data: { value: 'old' } }))
    storage.setItem(malformedKey, '{')

    expect(loadCachedDetailData(nodeLatencyCachePrefix, 'expired', '1h', validObject, now)).toBeNull()
    expect(loadCachedDetailData(nodeLatencyCachePrefix, 'malformed', '1h', validObject, now)).toBeNull()
    expect(storage.getItem(expiredKey)).toBeNull()
    expect(storage.getItem(malformedKey)).toBeNull()
  })

  it('bounds all detail entries together and evicts the least recently used entry', () => {
    const storage = new FakeSessionStorage()
    installStorage(storage)
    const base = 1_000_000
    for (let index = 0; index < detailSessionCacheMaxEntries; index += 1) {
      rememberDetailData(nodeLatencyCachePrefix, `node-${index}`, '1h', { value: String(index) }, base + index)
    }

    const touched = loadCachedDetailData(nodeLatencyCachePrefix, 'node-0', '1h', validObject, base + 100)
    expect(touched?.data).toEqual({ value: '0' })
    expect(touched?.stale).toBe(false)

    rememberDetailData(nodeLatencyCachePrefix, 'new-node', '1h', { value: 'new' }, base + 101)

    expect(storage.length).toBe(detailSessionCacheMaxEntries)
    expect(storage.getItem(detailCacheKey(nodeLatencyCachePrefix, 'node-0', '1h'))).not.toBeNull()
    expect(storage.getItem(detailCacheKey(nodeLatencyCachePrefix, 'node-1', '1h'))).toBeNull()
    expect(storage.getItem(detailCacheKey(nodeLatencyCachePrefix, 'new-node', '1h'))).not.toBeNull()
  })

  it('reports cached data as stale after the fresh TTL while retaining it until the max age', () => {
    const storage = new FakeSessionStorage()
    installStorage(storage)
    const storedAt = 1_000_000
    rememberDetailData(nodeLatencyCachePrefix, 'node', '1h', { value: 'cached' }, storedAt)

    expect(loadCachedDetailData(nodeLatencyCachePrefix, 'node', '1h', validObject, storedAt + detailCacheFreshTtlMs + 1)).toMatchObject({
      data: { value: 'cached' },
      storedAt,
      stale: true,
    })
  })

  it('evicts its own LRU entries and retries a QuotaExceeded write', () => {
    const storage = new FakeSessionStorage()
    installStorage(storage)
    const now = 1_000_000
    rememberDetailData(nodeLatencyCachePrefix, 'old-node', '1h', { value: 'old' }, now)
    storage.quotaFailuresRemaining = 1

    expect(() => rememberDetailData(nodeLatencyCachePrefix, 'new-node', '1h', { value: 'new' }, now + 1)).not.toThrow()
    expect(storage.getItem(detailCacheKey(nodeLatencyCachePrefix, 'old-node', '1h'))).toBeNull()
    expect(storage.getItem(detailCacheKey(nodeLatencyCachePrefix, 'new-node', '1h'))).not.toBeNull()
  })

  it('silently degrades if quota remains unavailable after all safe evictions', () => {
    const storage = new FakeSessionStorage()
    installStorage(storage)
    storage.setItem(detailCacheKey(nodeLatencyCachePrefix, 'old-node', '1h'), JSON.stringify({ storedAt: 1_000_000, lastAccessedAt: 1_000_000, data: { value: 'old' } }))
    storage.alwaysExceedQuota = true

    expect(() => rememberDetailData(nodeLatencyCachePrefix, 'new-node', '1h', { value: 'new' }, 1_000_001)).not.toThrow()
    expect(storage.getItem(detailCacheKey(nodeLatencyCachePrefix, 'new-node', '1h'))).toBeNull()
  })
})
