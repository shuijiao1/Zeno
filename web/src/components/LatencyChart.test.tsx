import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { LatencyChart, yDomainForRows } from './LatencyChart'

const points = [
  { ts: '2026-07-02T00:00:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 12, avgMs: 12, lossPercent: 0 },
  { ts: '2026-07-02T00:01:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 42, avgMs: 42, lossPercent: 25 },
  { ts: '2026-07-02T00:00:00Z', targetId: 'beta', targetName: 'Beta', medianMs: 20, avgMs: 20, lossPercent: 5 },
]

describe('LatencyChart', () => {
  it('renders a single custom hover surface without browser title tooltips', () => {
    const html = renderToStaticMarkup(<LatencyChart points={points} activeTargetIds={['alpha', 'beta']} />)

    expect(html).toContain('latency-hover-hit')
    expect(html.match(/latency-hover-hit/g)).toHaveLength(1)
    expect(html).not.toContain('<title>')
    expect(html).toContain('aria-label=')
    expect(html).toContain('延迟图表悬浮区域')
    expect(html).not.toContain('丢包 25.00%')
  })

  it('shows packet loss only for a single selected target, not when multiple latency lines are displayed', () => {
    const multiHtml = renderToStaticMarkup(<LatencyChart points={points} activeTargetIds={['alpha', 'beta']} />)
    const singleHtml = renderToStaticMarkup(<LatencyChart points={points} activeTargetIds={['alpha']} />)

    expect(multiHtml).not.toContain('packet-loss-area')
    expect(multiHtml).not.toContain('丢包')
    expect(singleHtml).toContain('packet-loss-area')
    expect(singleHtml).toContain('Alpha 丢包')
  })

  it('uses Kulin-style multi-tick x axis for day ranges instead of identical endpoint labels', () => {
    const dayPoints = Array.from({ length: 49 }, (_, index) => ({
      ts: new Date(Date.UTC(2026, 6, 5, 0, 30) + index * 30 * 60 * 1000).toISOString(),
      targetId: 'alpha',
      targetName: 'Alpha',
      medianMs: 20 + index,
      avgMs: 20 + index,
      lossPercent: 0,
    }))

    const html = renderToStaticMarkup(<LatencyChart points={dayPoints} activeTargetIds={['alpha']} />)
    const labels = [...html.matchAll(/class="axis-label"[^>]*>([^<]+)<\/text>/g)].map((match) => match[1])
    const xAxisLabels = labels.filter((label) => label.includes(':'))

    expect(xAxisLabels.length).toBeGreaterThan(2)
    expect(xAxisLabels.slice(1, -1).every((label) => !label.endsWith(':30'))).toBe(true)
    expect(new Set(xAxisLabels).size).toBeGreaterThan(2)
  })

  it('auto-scales the delay axis like Kulin instead of anchoring every selected target to zero', () => {
    const steadyHighLatencyPoints = [186.5, 187.2, 188.0].map((medianMs, index) => ({
      ts: new Date(Date.UTC(2026, 6, 5, 0, index * 30)).toISOString(),
      targetId: 'high',
      targetName: 'High latency',
      medianMs,
      avgMs: medianMs,
      lossPercent: 0,
    }))

    const html = renderToStaticMarkup(<LatencyChart points={steadyHighLatencyPoints} activeTargetIds={['high']} />)
    const labels = [...html.matchAll(/class="axis-label"[^>]*>([^<]+)<\/text>/g)].map((match) => match[1])
    const yAxisLabels = labels.filter((label) => label.endsWith('ms'))

    expect(yAxisLabels).not.toContain('0ms')
    expect(yAxisLabels.some((label) => label.startsWith('186') || label.startsWith('187') || label.startsWith('188'))).toBe(true)
  })

  it('caps drawn latency axis at 5000ms while still allowing values above the 1000ms timeout line', () => {
    const highLatencyPoints = [900, 2400, 6000].map((medianMs, index) => ({
      ts: new Date(Date.UTC(2026, 6, 5, 0, index * 30)).toISOString(),
      targetId: 'high',
      targetName: 'High latency',
      medianMs,
      avgMs: medianMs,
      lossPercent: 0,
    }))

    const html = renderToStaticMarkup(<LatencyChart points={highLatencyPoints} activeTargetIds={['high']} />)
    const labels = [...html.matchAll(/class="axis-label"[^>]*>([^<]+)<\/text>/g)].map((match) => match[1])
    const yAxisValues = labels
      .filter((label) => label.endsWith('ms'))
      .map((label) => Number(label.replace('ms', '')))

    expect(yAxisValues.reduce((max, value) => Math.max(max, value), Number.NEGATIVE_INFINITY)).toBe(5000)
    expect(yAxisValues.some((value) => value > 1000)).toBe(true)
  })

  it('breaks the line at null avg_ms samples without falling back to median_ms', () => {
    const gapPoints = [
      { ts: '2026-07-02T00:00:00Z', targetId: 'alpha', targetName: 'Alpha', avgMs: 12, medianMs: 10, lossPercent: 0 },
      { ts: '2026-07-02T00:01:00Z', targetId: 'alpha', targetName: 'Alpha', avgMs: null, medianMs: 999, lossPercent: 0 },
      { ts: '2026-07-02T00:02:00Z', targetId: 'alpha', targetName: 'Alpha', avgMs: 18, medianMs: 16, lossPercent: 0 },
    ]

    const html = renderToStaticMarkup(<LatencyChart points={gapPoints} activeTargetIds={['alpha']} />)
    const lineMatch = html.match(/<path[^>]+d="([^"]+)"[^>]+stroke="#22c55e"/)

    expect(lineMatch?.[1].match(/M/g)).toHaveLength(2)
    expect(lineMatch?.[1].match(/L/g) ?? []).toHaveLength(0)
  })

  it('keeps the full Kulin-style 1 day minute grid instead of thinning visible points', () => {
    const minutePoints = Array.from({ length: 1440 }, (_, index) => ({
      ts: new Date(Date.UTC(2026, 6, 5, 0, 0) + index * 60 * 1000).toISOString(),
      targetId: 'alpha',
      targetName: 'Alpha',
      avgMs: 20 + (index % 10),
      medianMs: 20 + (index % 10),
      lossPercent: 0,
    }))

    const html = renderToStaticMarkup(<LatencyChart points={minutePoints} activeTargetIds={['alpha']} />)
    const lineMatch = html.match(/<path[^>]+d="([^"]+)"[^>]+stroke="#22c55e"/)

    expect(lineMatch?.[1].match(/M/g)).toHaveLength(1)
    expect(lineMatch?.[1].match(/L/g)).toHaveLength(1439)
  })

  it('renders duplicate and reserved-looking target names as labels while selecting lines by id', () => {
    const timestamp = '2026-07-02T00:00:00Z'
    const specialPoints = [
      { ts: timestamp, targetId: 'first-id', targetName: 'Duplicate', avgMs: 11, medianMs: 11, lossPercent: 0 },
      { ts: timestamp, targetId: 'second-id', targetName: 'Duplicate', avgMs: 22, medianMs: 22, lossPercent: 0 },
      { ts: timestamp, targetId: 'created-id', targetName: 'created_at', avgMs: 33, medianMs: 33, lossPercent: 0 },
      { ts: timestamp, targetId: 'loss-id', targetName: 'first-id_packet_loss', avgMs: 44, medianMs: 44, lossPercent: 0 },
    ]

    const html = renderToStaticMarkup(
      <LatencyChart points={specialPoints} activeTargetIds={specialPoints.map((point) => point.targetId)} />,
    )

    expect(html.match(/>Duplicate<\/span>/g)).toHaveLength(2)
    expect(html).toContain('>created_at</span>')
    expect(html).toContain('>first-id_packet_loss</span>')
    expect(html.match(/<path[^>]+vector-effect="non-scaling-stroke"/g)).toHaveLength(4)
  })

  it('reduces an exceptionally large latency payload without spreading it into Math.min or Math.max', () => {
    const rows = Array.from({ length: 250_000 }, (_, index) => ({ created_at: index, target: index % 2 === 0 ? 12 : 48 }))

    expect(() => yDomainForRows(rows, ['target'])).not.toThrow()
    const domain = yDomainForRows(rows, ['target'])
    expect(domain.min).toBeCloseTo(6.6)
    expect(domain.max).toBeCloseTo(53.4)
  })
})
