import { afterEach, describe, expect, it } from 'vitest'
import { extractSafeCustomCSS } from './customCode'
import { availableHistoryRanges, coerceHistoryRange } from './historyRange'
import { shouldStartHttpFallback } from './liveFallback'
import { loadStoredSummary, rememberSummary, summaryFreshTtlMs } from './summaryCache'
import type { SummaryData } from '../api/client'

const summary: SummaryData = {
  nodes: [],
  services: [],
  latencyPoints: [],
}

function installWindowStorage() {
  const storage = new Map<string, string>()
  const windowStub = {
    localStorage: {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => storage.set(key, value),
      removeItem: (key: string) => storage.delete(key),
    },
  }
  const previousWindow = globalThis.window
  Object.defineProperty(globalThis, 'window', { value: windowStub, configurable: true })
  return () => Object.defineProperty(globalThis, 'window', { value: previousWindow, configurable: true })
}

describe('realtime reliability helpers', () => {
  afterEach(() => {
    // Ensure tests that stubbed window do not leak a storage object.
    if (typeof window !== 'undefined' && !('document' in window)) {
      Reflect.deleteProperty(globalThis, 'window')
    }
  })

  it('marks stored summary data stale after the short freshness TTL', () => {
    const restore = installWindowStorage()
    try {
      rememberSummary(summary, 1_000)
      expect(loadStoredSummary(1_000 + summaryFreshTtlMs - 1)).toMatchObject({ data: summary, stale: false, storedAt: 1_000 })
      expect(loadStoredSummary(1_000 + summaryFreshTtlMs + 1)).toMatchObject({ data: summary, stale: true, storedAt: 1_000 })
    } finally {
      restore()
    }
  })

  it('starts HTTP fallback only when no WS frame arrived yet and no fallback is running', () => {
    expect(shouldStartHttpFallback(false, false)).toBe(true)
    expect(shouldStartHttpFallback(true, false)).toBe(false)
    expect(shouldStartHttpFallback(false, true)).toBe(false)
  })

  it('limits unauthenticated history ranges to realtime and one day', () => {
    expect(availableHistoryRanges(false).map((option) => option.value)).toEqual(['1h', '1d'])
    expect(availableHistoryRanges(true).map((option) => option.value)).toEqual(['1h', '1d', '7d', '30d'])
    expect(coerceHistoryRange('30d', false, '1d')).toBe('1d')
    expect(coerceHistoryRange('30d', true, '1d')).toBe('30d')
  })

  it('keeps appearance CSS but strips executable custom code', () => {
    const css = extractSafeCustomCSS('<style>.home-top-card { border-color: #2563eb; background: url(javascript:alert(1)); }</style><img onerror="alert(1)"><script>alert(1)</script>')
    expect(css).toContain('.home-top-card { border-color: #2563eb;')
    expect(css).not.toContain('<script>')
    expect(css).not.toContain('onerror')
    expect(css).not.toContain('javascript:alert')
    expect(css).toContain('url(about:blank)')
  })
})
