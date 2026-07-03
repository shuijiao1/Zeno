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
})

describe('state history layout', () => {
  it('keeps resource history as compact cards and stacks them on phones', () => {
    expect(styles).toContain('.state-history-grid')
    expect(styles).toContain('grid-template-columns: repeat(4, minmax(0, 1fr))')
    expect(styles).toContain('grid-template-columns: repeat(2, minmax(0, 1fr))')
  })

  it('keeps uptime as a compact header badge', () => {
    expect(styles).toContain('.state-uptime')
    expect(styles).toContain('border-radius: 999px')
  })
})
