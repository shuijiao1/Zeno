// @ts-nocheck
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { describe, expect, it } from 'vitest'

const stylesPath = join(dirname(fileURLToPath(import.meta.url)), 'styles.css')
const styles = readFileSync(stylesPath, 'utf8')

describe('mobile latency target layout', () => {
  it('keeps latency target buttons compact at three per row on phones', () => {
    expect(styles).toContain('@media (max-width: 767px)')
    expect(styles).toContain('.latency-target-grid button')
    expect(styles).toContain('flex: 0 0 calc(100% / 3)')
  })
})

describe('homepage and admin shell layout', () => {
  it('keeps homepage chrome and overview inside one shared card shell', () => {
    expect(styles).toContain('.home-top-card')
    expect(styles).toContain('.home-top-card .kulin-nav')
    expect(styles).toContain('.home-top-card .server-overview')
  })

  it('uses a compact homepage summary without duplicate server-stat tiles', () => {
    expect(styles).toContain('.home-summary__compact')
    expect(styles).toContain('grid-template-columns: minmax(200px, .72fr) minmax(0, 1.28fr)')
    expect(styles).toContain('.home-network-grid { grid-template-columns: repeat(4, minmax(0, 1fr)); }')
    expect(styles).toContain('.home-summary__compact { grid-template-columns: minmax(108px, .78fr) minmax(0, 1.22fr); gap: 8px; }')
    expect(styles).toContain('.home-network-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 6px; }')
    expect(styles).not.toContain('.home-stat-grid')
  })

  it('styles the backend/admin shell with the same card language as the front page', () => {
    expect(styles).toContain('.admin-panel')
    expect(styles).toContain('.admin-action-card')
    expect(styles).toContain('background: var(--card)')
  })

  it('keeps authenticated node management in compact card shells', () => {
    expect(styles).toContain('.admin-login-card')
    expect(styles).toContain('.admin-node-section')
    expect(styles).toContain('.admin-node-card')
    expect(styles).toContain('.admin-node-status')
  })

  it('lets admin lists size to content without internal scroll containers', () => {
    expect(styles).toContain('.admin-workspace-panel .admin-list')
    expect(styles).toContain('max-height: none')
    expect(styles).toContain('overflow: visible')
    expect(styles).toContain('scrollbar-gutter: auto')
    expect(styles).toContain('padding: 7px 10px')
    expect(styles).toContain('min-height: 58px')
    expect(styles).toContain('.admin-ip-stack')
    expect(styles).toContain('height: 28px')
    expect(styles).not.toContain('max-height: calc(100dvh - 300px)')
    expect(styles).not.toContain('max-height: calc(100dvh - 260px)')
  })
})

describe('state history layout', () => {
  it('keeps the detail hero dense with Nezha-like inline facts, not nested metric cards', () => {
    expect(styles).toContain('.detail-fact-strip')
    expect(styles).toContain('.detail-status-pill')
    expect(styles).toContain('.detail-fact.is-wide')
    expect(styles).not.toContain('.detail-info-card')
  })

  it('renders resource history as separated full-width chart cards on phones too', () => {
    expect(styles).toContain('.state-history-stack')
    expect(styles).toContain('flex-direction: column')
    expect(styles).toContain('.state-history-chart-card')
    expect(styles).toContain('.state-sparkline--large')
  })

  it('keeps uptime as a compact header badge', () => {
    expect(styles).toContain('.state-uptime')
    expect(styles).toContain('border-radius: 999px')
  })
})

describe('visual weight polish', () => {
  it('avoids heavy UI font weights in the Nezha-like shell', () => {
    expect(styles).not.toContain('font-weight: 700')
    expect(styles).not.toContain('font-weight: 750')
    expect(styles).not.toContain('font-weight: 760')
    expect(styles).not.toContain('font-weight: 780')
  })

  it('keeps custom icon strokes and resource curves lighter than the default heavy Lucide stroke', () => {
    expect(styles).toContain('stroke-width: 1.75')
    expect(styles).toContain('.detail-title-button svg')
    expect(styles).toContain('stroke-width: 1.6')
    expect(styles).toContain('.state-sparkline__line { fill: none; stroke-width: 1.35')
  })
})
