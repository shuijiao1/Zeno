import type { SummaryData } from '../api/client'

export const summaryCacheKey = 'zeno_summary_cache_v2'
export const legacySummaryCacheKey = 'zeno_summary_cache_v1'
export const summaryFreshTtlMs = 30 * 1000
export const summaryCacheMaxAgeMs = 24 * 60 * 60 * 1000

type SummaryCachePayload = {
  storedAt: number
  data: SummaryData
}

export type StoredSummary = {
  data: SummaryData
  storedAt: number
  stale: boolean
}

function validateSummaryData(value: unknown): SummaryData | null {
  const data = value as Partial<SummaryData> | null
  if (!data || !Array.isArray(data.nodes) || !Array.isArray(data.services)) return null
  return {
    nodes: data.nodes as SummaryData['nodes'],
    services: data.services as SummaryData['services'],
    latencyPoints: Array.isArray(data.latencyPoints) ? data.latencyPoints as SummaryData['latencyPoints'] : [],
  }
}

function parseSummaryPayload(raw: string, now: number): StoredSummary | null {
  const parsed = JSON.parse(raw) as Partial<SummaryCachePayload> | Partial<SummaryData>
  const hasStoredAt = typeof (parsed as Partial<SummaryCachePayload>).storedAt === 'number'
  const storedAt = hasStoredAt ? Number((parsed as Partial<SummaryCachePayload>).storedAt) : 0
  const data = validateSummaryData(hasStoredAt ? (parsed as Partial<SummaryCachePayload>).data : parsed)
  if (!data) return null
  if (storedAt > 0 && now - storedAt > summaryCacheMaxAgeMs) return null
  return { data, storedAt, stale: storedAt <= 0 || now - storedAt > summaryFreshTtlMs }
}

export function loadStoredSummary(now = Date.now()): StoredSummary | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = window.localStorage.getItem(summaryCacheKey)
    if (raw) return parseSummaryPayload(raw, now)
    const legacyRaw = window.localStorage.getItem(legacySummaryCacheKey)
    return legacyRaw ? parseSummaryPayload(legacyRaw, now) : null
  } catch {
    return null
  }
}

export function rememberSummary(summary: SummaryData, now = Date.now()) {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(summaryCacheKey, JSON.stringify({ storedAt: now, data: summary } satisfies SummaryCachePayload))
    window.localStorage.removeItem(legacySummaryCacheKey)
  } catch {}
}

export function formatCacheTimestamp(storedAt: number): string {
  if (storedAt <= 0) return '未知'
  return new Date(storedAt).toLocaleString('zh-CN', { hour12: false })
}
