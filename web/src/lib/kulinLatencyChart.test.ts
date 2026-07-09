import { describe, expect, it } from 'vitest'
import {
  applyKulinPeakCut,
  buildKulinChartRows,
  buildKulinTargetSeries,
  calculateKulinPacketLoss,
  selectKulinChartView,
} from './kulinLatencyChart'

describe('calculateKulinPacketLoss', () => {
  it('matches Kulin packet-loss smoothing for zero and timeout-like delays', () => {
    const losses = calculateKulinPacketLoss([15, 18, 0, 45])

    expect(losses).toEqual([0, 0, 30, 21])
  })
})

describe('buildKulinTargetSeries', () => {
  it('uses avg_ms as Kulin avg_delay and draws missing avg_ms as 0 without median fallback', () => {
    const series = buildKulinTargetSeries([
      { ts: '2026-07-02T00:00:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 12, avgMs: 14, lossPercent: 0 },
      { ts: '2026-07-02T00:01:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 99, lossPercent: 100 },
    ])

    expect(series[0].points.map((point) => point.avg_delay)).toEqual([14, 0])
    expect(series[0].points.map((point) => point.packet_loss)).toEqual([0, 100])
  })
})

describe('buildKulinChartRows', () => {
  it('merges target series by timestamp using monitor names as Kulin chart data keys', () => {
    const series = buildKulinTargetSeries([
      { ts: '2026-07-02T00:00:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 12, avgMs: 12, lossPercent: 0 },
      { ts: '2026-07-02T00:01:00Z', targetId: 'beta', targetName: 'Beta', medianMs: 20, avgMs: 20, lossPercent: 5 },
    ])

    expect(buildKulinChartRows(series)).toEqual([
      { created_at: Date.parse('2026-07-02T00:00:00Z'), Alpha: 12, Alpha_packet_loss: 0, Beta: null, Beta_packet_loss: null },
      { created_at: Date.parse('2026-07-02T00:01:00Z'), Alpha: null, Alpha_packet_loss: null, Beta: 20, Beta_packet_loss: 5 },
    ])
  })
})

describe('selectKulinChartView', () => {
  const series = buildKulinTargetSeries([
    { ts: '2026-07-02T00:00:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 12, avgMs: 12, lossPercent: 0 },
    { ts: '2026-07-02T00:00:00Z', targetId: 'beta', targetName: 'Beta', medianMs: 20, avgMs: 20, lossPercent: 5 },
  ])
  const rows = buildKulinChartRows(series)

  it('shows all delay lines by default like Kulin', () => {
    const view = selectKulinChartView(series, rows, [])

    expect(view.lineKeys).toEqual(['Alpha', 'Beta'])
    expect(view.showPacketLossArea).toBe(false)
  })

  it('shows selected delay plus packet-loss area when exactly one monitor is active', () => {
    const view = selectKulinChartView(series, rows, ['Alpha'])

    expect(view.lineKeys).toEqual(['avg_delay'])
    expect(view.showPacketLossArea).toBe(true)
    expect(view.rows).toEqual([{ created_at: Date.parse('2026-07-02T00:00:00Z'), avg_delay: 12, packet_loss: 0 }])
  })
})

describe('applyKulinPeakCut', () => {
  it('uses Kulin 11-point MAD/EWMA smoothing to suppress a single spike', () => {
    const rows = Array.from({ length: 11 }, (_, index) => ({
      created_at: index,
      Alpha: index === 10 ? 1000 : 10,
    }))

    const processed = applyKulinPeakCut(rows, ['Alpha'])

    expect(processed.slice(0, 10).map((row) => row.Alpha)).toEqual(Array(10).fill(10))
    expect(processed[10].Alpha).toBe(10)
  })
})
