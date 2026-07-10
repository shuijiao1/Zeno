export type CachedDetail<T> = {
  data: T
  storedAt: number
  stale: boolean
}

export const nodeLatencyCachePrefix = 'zeno_node_latency_cache_v1'
export const nodeStateCachePrefix = 'zeno_node_state_cache_v1'
export const serviceLatencyCachePrefix = 'zeno_service_latency_cache_v1'
export const detailCacheFreshTtlMs = 30 * 1000
export const detailCacheMaxAgeMs = 5 * 60 * 1000
export const detailCacheMaxBytes = 700_000

export function detailCacheKey(prefix: string, resourceId: string, range: string): string {
  return `${prefix}:${resourceId}:${range}`
}

export function loadCachedDetailData<T>(prefix: string, resourceId: string, range: string, validate: (value: unknown) => T | null, now = Date.now()): CachedDetail<T> | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = window.sessionStorage.getItem(detailCacheKey(prefix, resourceId, range))
    if (!raw) return null
    const parsed = JSON.parse(raw) as { storedAt?: number; data?: unknown }
    const storedAt = Number(parsed.storedAt ?? 0)
    if (!Number.isFinite(storedAt) || storedAt <= 0 || now - storedAt > detailCacheMaxAgeMs) return null
    const data = validate(parsed.data)
    if (!data) return null
    return { data, storedAt, stale: now - storedAt > detailCacheFreshTtlMs }
  } catch {
    return null
  }
}

export function rememberDetailData(prefix: string, resourceId: string, range: string, data: unknown, now = Date.now()) {
  if (typeof window === 'undefined') return
  try {
    const payload = JSON.stringify({ storedAt: now, data })
    if (payload.length > detailCacheMaxBytes) return
    window.sessionStorage.setItem(detailCacheKey(prefix, resourceId, range), payload)
  } catch {}
}
