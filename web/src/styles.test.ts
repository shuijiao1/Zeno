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
})

describe('state history layout', () => {
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
