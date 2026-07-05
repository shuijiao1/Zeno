import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { LatencyChart } from './LatencyChart'

const points = [
  { ts: '2026-07-02T00:00:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 12, lossPercent: 0 },
  { ts: '2026-07-02T00:01:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 42, lossPercent: 25 },
  { ts: '2026-07-02T00:00:00Z', targetId: 'beta', targetName: 'Beta', medianMs: 20, lossPercent: 5 },
]

describe('LatencyChart', () => {
  it('renders immediate custom hover columns without browser title tooltips', () => {
    const html = renderToStaticMarkup(<LatencyChart points={points} activeTargetNames={['Alpha', 'Beta']} />)

    expect(html).toContain('latency-hover-column')
    expect(html).toContain('latency-hover-guide')
    expect(html).toContain('latency-hover-hit')
    expect(html).not.toContain('<title>')
    expect(html).toContain('aria-label=')
    expect(html).toContain('Alpha · 42ms')
    expect(html).toContain('Beta · 20ms')
    expect(html).not.toContain('丢包 25.00%')
  })

  it('shows packet loss only for a single selected target, not when multiple latency lines are displayed', () => {
    const multiHtml = renderToStaticMarkup(<LatencyChart points={points} activeTargetNames={['Alpha', 'Beta']} />)
    const singleHtml = renderToStaticMarkup(<LatencyChart points={points} activeTargetNames={['Alpha']} />)

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
      lossPercent: 0,
    }))

    const html = renderToStaticMarkup(<LatencyChart points={dayPoints} activeTargetNames={['Alpha']} />)
    const labels = [...html.matchAll(/class="axis-label"[^>]*>([^<]+)<\/text>/g)].map((match) => match[1])
    const xAxisLabels = labels.filter((label) => label.includes(':'))

    expect(xAxisLabels.length).toBeGreaterThan(2)
    expect(xAxisLabels.every((label) => !label.endsWith(':30'))).toBe(true)
    expect(new Set(xAxisLabels).size).toBeGreaterThan(2)
  })

  it('auto-scales the delay axis like Kulin instead of anchoring every selected target to zero', () => {
    const steadyHighLatencyPoints = [186.5, 187.2, 188.0].map((medianMs, index) => ({
      ts: new Date(Date.UTC(2026, 6, 5, 0, index * 30)).toISOString(),
      targetId: 'high',
      targetName: 'High latency',
      medianMs,
      lossPercent: 0,
    }))

    const html = renderToStaticMarkup(<LatencyChart points={steadyHighLatencyPoints} activeTargetNames={['High latency']} />)
    const labels = [...html.matchAll(/class="axis-label"[^>]*>([^<]+)<\/text>/g)].map((match) => match[1])
    const yAxisLabels = labels.filter((label) => label.endsWith('ms'))

    expect(yAxisLabels).not.toContain('0ms')
    expect(yAxisLabels.some((label) => label.startsWith('186') || label.startsWith('187') || label.startsWith('188'))).toBe(true)
  })

  it('keeps all-line charts readable by not letting a few spikes dominate the y axis', () => {
    const readablePoints = Array.from({ length: 100 }, (_, index) => ([
      {
        ts: new Date(Date.UTC(2026, 6, 5, 0, index)).toISOString(),
        targetId: 'low',
        targetName: 'Low',
        avgMs: index === 50 ? 604 : 12,
        medianMs: index === 50 ? 500 : 10,
        lossPercent: 0,
      },
      {
        ts: new Date(Date.UTC(2026, 6, 5, 0, index)).toISOString(),
        targetId: 'high',
        targetName: 'High',
        avgMs: 180,
        medianMs: 175,
        lossPercent: 0,
      },
    ])).flat()

    const html = renderToStaticMarkup(<LatencyChart points={readablePoints} />)
    const labels = [...html.matchAll(/class="axis-label"[^>]*>([^<]+)<\/text>/g)].map((match) => match[1])
    const yAxisLabels = labels.filter((label) => label.endsWith('ms')).map((label) => Number.parseInt(label, 10))

    expect(Math.max(...yAxisLabels)).toBeLessThan(260)
    expect(html).toContain('clip-path="url(#latency-plot-')
  })
})
