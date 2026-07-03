import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { LatencyChart } from './LatencyChart'

const points = [
  { ts: '2026-07-02T00:00:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 12, lossPercent: 0 },
  { ts: '2026-07-02T00:01:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 42, lossPercent: 25 },
  { ts: '2026-07-02T00:00:00Z', targetId: 'beta', targetName: 'Beta', medianMs: 20, lossPercent: 5 },
]

describe('LatencyChart', () => {
  it('renders hover guide columns with a vertical line and latency-only titles', () => {
    const html = renderToStaticMarkup(<LatencyChart points={points} activeTargetNames={['Alpha', 'Beta']} />)

    expect(html).toContain('latency-hover-column')
    expect(html).toContain('latency-hover-guide')
    expect(html).toContain('<title>')
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
})
