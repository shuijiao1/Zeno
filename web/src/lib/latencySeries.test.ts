import { describe, expect, it } from 'vitest'
import { buildLatencySeries, yDomain } from './latencySeries'

describe('buildLatencySeries', () => {
  it('keeps each latency target as a separate thin-line series', () => {
    const points = [
      { ts: '2026-07-02T12:00:00Z', targetId: 'google', targetName: 'Google', medianMs: 10, lossPercent: 0 },
      { ts: '2026-07-02T12:00:00Z', targetId: 'telegram', targetName: 'Telegram', medianMs: 80, lossPercent: 0 },
      { ts: '2026-07-02T12:01:00Z', targetId: 'google', targetName: 'Google', medianMs: 120, lossPercent: 0 },
    ]

    const series = buildLatencySeries(points)

    expect(series.map((item) => item.targetId)).toEqual(['google', 'telegram'])
    expect(series[0].points.map((point) => point.medianMs)).toEqual([10, 120])
    expect(series[0].strokeWidth).toBe(1)
  })

  it('keeps loss-only points as null latency instead of converting them to 0ms', () => {
    const series = buildLatencySeries([
      { ts: '2026-07-02T12:00:00Z', targetId: 'google', targetName: 'Google', medianMs: null, lossPercent: 100 },
    ])

    expect(series[0].points[0].medianMs).toBeNull()
    expect(series[0].points[0].lossPercent).toBe(100)
  })
})

describe('yDomain', () => {
  it('uses only non-null latency and leaves headroom for spikes', () => {
    const domain = yDomain([
      { ts: 'a', targetId: 'a', targetName: 'A', medianMs: null, lossPercent: 100 },
      { ts: 'b', targetId: 'a', targetName: 'A', medianMs: 10, lossPercent: 0 },
      { ts: 'c', targetId: 'a', targetName: 'A', medianMs: 100, lossPercent: 0 },
    ])

    expect(domain.min).toBe(0)
    expect(domain.max).toBe(120)
  })
})
