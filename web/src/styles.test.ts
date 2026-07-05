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
    expect(styles).toContain('.home-top-card .home-summary')
  })

  it('uses a compact homepage summary without duplicate title/online-rate tiles', () => {
    expect(styles).toContain('.home-summary__status-line')
    expect(styles).toContain('.home-summary__metrics')
    expect(styles).toContain('.home-summary__metrics > div')
    expect(styles).not.toContain('.home-summary__compact')
    expect(styles).not.toContain('.home-network-grid')
    expect(styles).not.toContain('.home-stat-grid')
  })

  it('styles the backend/admin shell with the same card language as the front page', () => {
    expect(styles).toContain('.admin-panel')
    expect(styles).toContain('.admin-workspace-panel .admin-list')
    expect(styles).toContain('background: var(--card)')
  })

  it('keeps the homepage backend entry visible on phones', () => {
    expect(styles).toContain('@media (max-width: 767px)')
    expect(styles).toContain('.login-link,')
    expect(styles).toContain('.nav-logout-button')
    expect(styles).toContain('min-height: 32px')
    expect(styles).not.toContain('.login-link { display: none; }')
  })

  it('keeps authenticated node management in compact lists without old card shells', () => {
    expect(styles).toContain('.admin-login-card')
    expect(styles).toContain('.admin-node-section')
    expect(styles).toContain(".admin-section-nav button[data-active='true']")
    expect(styles).toContain('.admin-node-status')
    expect(styles).toContain('.admin-server-sort-list')
    expect(styles).toContain('.admin-server-sort-item')
    expect(styles).toContain('cursor: grab')
    expect(styles).not.toContain('.admin-node-card')
    expect(styles).not.toContain('.admin-target-card')
    expect(styles).not.toContain('.admin-node-grid')
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
    expect(styles).toContain('repeat(auto-fit, minmax(96px, 1fr))')
    expect(styles).toContain('.admin-node-section .admin-list-row,')
    expect(styles).toContain('.admin-target-list .admin-list-row,')
    expect(styles).toContain('overflow-wrap: anywhere')
    expect(styles).not.toContain('max-height: calc(100dvh - 300px)')
    expect(styles).not.toContain('max-height: calc(100dvh - 260px)')
    expect(styles).not.toContain('.overview-card')
    expect(styles).not.toContain('.server-overview')
  })
})

describe('state history layout', () => {
  it('keeps the detail hero dense with Nezha-like inline facts, not nested metric cards', () => {
    expect(styles).toContain('.detail-fact-strip')
    expect(styles).toContain('.detail-status-pill')
    expect(styles).toContain('.detail-fact.is-wide')
    expect(styles).not.toContain('.detail-info-card')
  })

  it('renders resource history as a compact chart grid like Kulin on desktop', () => {
    expect(styles).toContain('.state-history-stack')
    expect(styles).toContain('grid-template-columns: repeat(3, minmax(0, 1fr))')
    expect(styles).toContain('grid-template-columns: 1fr')
    expect(styles).toContain('.state-history-chart-card')
    expect(styles).toContain('.state-sparkline--large')
  })

  it('keeps uptime and load as compact badges in the top server card', () => {
    expect(styles).toContain('.detail-hero__badges')
    expect(styles).toContain('.detail-hero-badge')
    expect(styles).toContain('border-radius: 999px')
  })

  it('keeps offline cards frozen without grayscale filtering', () => {
    expect(styles).toContain('.kulin-node-card.is-offline')
    expect(styles).toContain('.node-offline-watermark')
    expect(styles).not.toContain('filter: grayscale')
  })
})

describe('Kulin-inspired color polish', () => {
  it('uses an aurora glass palette instead of a flat grey shell', () => {
    expect(styles).toContain('--zeno-glow-primary')
    expect(styles).toContain('--zeno-glow-secondary')
    expect(styles).toContain('var(--zeno-desktop-background-image, none),')
    expect(styles).toContain('linear-gradient(135deg, var(--zeno-bg-a), var(--zeno-bg-b)')
    expect(styles).toContain('blur(18px) saturate(1.35)')
  })

  it('gives important controls and chart cards visible accent colors', () => {
    expect(styles).toContain('--zeno-accent-gradient')
    expect(styles).toContain('.home-summary__status-line strong')
    expect(styles).toContain('.admin-section-nav button[data-active=\'true\']')
    expect(styles).toContain('.state-history-chart-card.tone-green')
    expect(styles).toContain('.state-history-chart-card::before')
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
