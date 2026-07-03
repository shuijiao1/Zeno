import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { LatencyChart } from './LatencyChart'

const points = [
  { ts: '2026-07-02T00:00:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 12, lossPercent: 0 },
  { ts: '2026-07-02T00:01:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 42, lossPercent: 25 },
  { ts: '2026-07-02T00:00:00Z', targetId: 'beta', targetName: 'Beta', medianMs: 20, lossPercent: 5 },
]

describe('LatencyChart', () => {
  it('renders hover titles with target latency values on curve hit points', () => {
    const html = renderToStaticMarkup(<LatencyChart points={points} activeTargetNames={['Alpha']} />)

    expect(html).toContain('latency-hover-point')
    expect(html).toContain('<title>Alpha · 42ms · 丢包 25.00%')
  })

  it('renders a Nezha-like yellow packet-loss background in the chart', () => {
    const html = renderToStaticMarkup(<LatencyChart points={points} />)

    expect(html).toContain('packet-loss-area')
    expect(html).toContain('丢包')
  })
})
