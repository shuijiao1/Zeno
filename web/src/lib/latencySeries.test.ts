import { describe, expect, it } from 'vitest'
import { buildLatencySeries, peakCutLatencyPoints, yDomain } from './latencySeries'

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

  it('can cut one-off spikes from the y-axis while preserving normal samples', () => {
    const domain = yDomain([
      { ts: 'a', targetId: 'a', targetName: 'A', medianMs: 20, lossPercent: 0 },
      { ts: 'b', targetId: 'a', targetName: 'A', medianMs: 22, lossPercent: 0 },
      { ts: 'c', targetId: 'a', targetName: 'A', medianMs: 19, lossPercent: 0 },
      { ts: 'd', targetId: 'a', targetName: 'A', medianMs: 1000, lossPercent: 0 },
    ], { peakCut: true })

    expect(domain.min).toBe(0)
    expect(domain.max).toBe(27)
  })
})

describe('peakCutLatencyPoints', () => {
  it('caps visible spike values but keeps null loss points untouched', () => {
    const points = peakCutLatencyPoints([
      { ts: 'a', targetId: 'a', targetName: 'A', medianMs: 20, lossPercent: 0 },
      { ts: 'b', targetId: 'a', targetName: 'A', medianMs: null, lossPercent: 100 },
      { ts: 'c', targetId: 'a', targetName: 'A', medianMs: 1000, lossPercent: 0 },
    ], { minSamples: 3 })

    expect(points.map((point) => point.medianMs)).toEqual([20, null, 24])
    expect(points[2].lossPercent).toBe(0)
  })
})
