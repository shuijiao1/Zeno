// @ts-nocheck
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { describe, expect, it } from 'vitest'

const stylesPath = join(dirname(fileURLToPath(import.meta.url)), 'styles.css')
const styles = readFileSync(stylesPath, 'utf8')

describe('mobile latency target layout', () => {
  it('keeps latency target buttons compact as card tiles on phones', () => {
    expect(styles).toContain('@media (max-width: 767px)')
    expect(styles).toContain('grid-template-columns: repeat(auto-fit, minmax(150px, 1fr))')
    expect(styles).toContain('.latency-target-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); padding: 0 12px 12px; }')
    expect(styles).toContain('.latency-target-grid button')
    expect(styles).toContain('border-radius: var(--radius-field)')
    expect(styles).toContain('.state-sparkline__axis { fill: color-mix(in srgb, var(--foreground) 52%, transparent); font-size: 34px; font-weight: 500; }')
    expect(styles).toContain('.state-sparkline__time-axis { fill: color-mix(in srgb, var(--foreground) 42%, transparent); font-size: 30px; font-weight: 500; }')
  })
})

describe('homepage and admin shell layout', () => {
  it('keeps homepage chrome and overview inside one shared card shell', () => {
    expect(styles).toContain('.home-top-card')
    expect(styles).toContain('.home-top-card .kulin-nav')
    expect(styles).toContain('.home-top-card .home-summary')
  })

  it('keeps mobile homepage server cards aligned to the top card width', () => {
    expect(styles).toContain('@media (max-width: 767px)')
    expect(styles).toContain('.kulin-node-card { max-width: none; min-height: 394px; }')
  })

  it('uses a compact homepage summary without duplicate title/online-rate tiles', () => {
    expect(styles).toContain('.home-summary__status-line')
    expect(styles).toContain('.home-summary__metrics')
    expect(styles).toContain('.home-summary__metrics > div')
    expect(styles).not.toContain('.home-summary__compact')
    expect(styles).not.toContain('.home-network-grid')
    expect(styles).not.toContain('.home-stat-grid')
  })

  it('styles the backend/admin shell with the same translucent card language as the front page', () => {
    expect(styles).toContain('.admin-panel')
    expect(styles).toContain('.admin-workspace-panel .admin-list')
    expect(styles).toContain('--surface-strong')
    expect(styles).toContain('background: var(--surface-strong)')
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
    expect(styles).toContain('padding: 10px 12px')
    expect(styles).toContain('min-height: 64px')
    expect(styles).toContain('.admin-ip-stack')
    expect(styles).toContain('height: 34px')
    expect(styles).toContain('repeat(auto-fit, minmax(96px, 1fr))')
    expect(styles).toContain('.admin-node-section .admin-list-row,')
    expect(styles).toContain('.admin-target-list .admin-list-row,')
    expect(styles).toContain('overflow-wrap: anywhere')
    expect(styles).not.toContain('max-height: calc(100dvh - 300px)')
    expect(styles).not.toContain('max-height: calc(100dvh - 260px)')
    expect(styles).not.toContain('.overview-card')
    expect(styles).not.toContain('.server-overview')
  })

  it('simplifies backend chrome and gives secondary forms card-aligned sections', () => {
    expect(styles).toContain('background: transparent')
    expect(styles).toContain('box-shadow: none')
    expect(styles).toContain('font-size: 23px')
    expect(styles).toContain('font-size: 15px')
    expect(styles).toContain('.admin-account-section,')
    expect(styles).toContain('.admin-form-section')
    expect(styles).toContain('.admin-form-section-title::before')
    expect(styles).toContain('border-radius: var(--radius-card)')
    expect(styles).toContain('.admin-segmented-options')
    expect(styles).toContain('.admin-modal-body')
    expect(styles).toContain('clip-path: inset(0 round var(--radius-panel))')
    expect(styles).toContain('.admin-notification-list .admin-node-status')
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
    expect(styles).toContain('grid-template-columns: repeat(auto-fit, minmax(280px, 1fr))')
    expect(styles).toContain('grid-template-columns: 1fr')
    expect(styles).toContain('.state-history-chart-card')
    expect(styles).toContain('background: var(--card)')
    expect(styles).toContain('.state-sparkline--large')
    expect(styles).toContain('.state-history-chart-card .state-sparkline--large { flex: 1 1 auto; min-height: 112px; }')
    expect(styles).toContain('.state-sparkline__area { opacity: .18; stroke: none; pointer-events: none; }')
  })

  it('keeps uptime and load as compact pill badges in the top server card', () => {
    expect(styles).toContain('.detail-hero__badges')
    expect(styles).toContain('.detail-hero-badge')
    expect(styles).toContain('border-radius: var(--radius-pill)')
  })

  it('keeps offline cards frozen without grayscale filtering', () => {
    expect(styles).toContain('.kulin-node-card.is-offline')
    expect(styles).toContain('.node-offline-watermark')
    expect(styles).not.toContain('filter: grayscale')
  })

  it('keeps server-card usage tracks visible on white cards', () => {
    expect(styles).toContain('--usage-track-bg')
    expect(styles).toContain('--usage-track-border')
    expect(styles).toContain('background: var(--usage-track-bg)')
  })
})

describe('Kulin-inspired color polish', () => {
  it('keeps the overall shell and cards pure white instead of aurora/grey backgrounds', () => {
    expect(styles).toContain('--background: #ffffff')
    expect(styles).toContain('--card: #ffffff')
    expect(styles).toContain('background-image: var(--zeno-desktop-background-image, none)')
    expect(styles).not.toContain('--zeno-glow-primary')
    expect(styles).not.toContain('radial-gradient(circle at')
    expect(styles).not.toContain('linear-gradient(135deg, var(--zeno-bg-a)')
  })

  it('uses grey labels with black regular values and only emphasizes latency numbers', () => {
    expect(styles).toContain('.home-summary__status-line strong')
    expect(styles).toContain('.home-summary__metrics dd')
    expect(styles).toContain('.metric-label { color: var(--muted)')
    expect(styles).toContain('.node-metric strong')
    expect(styles).toContain('.node-metric strong { min-width: 0; margin-left: auto; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--foreground); font-weight: 500')
    expect(styles).toContain('.metric-latency strong { font-weight: 600; }')
    expect(styles).toContain('.latency-target-grid strong { color: var(--foreground); font-size: 20px')
    expect(styles).toContain('font-weight: 600; letter-spacing: -0.03em')
    expect(styles.match(/font-weight: 600/g) ?? []).toHaveLength(2)
    expect(styles).toContain('.latency-target-grid em { color: var(--muted)')
    expect(styles).toContain('.admin-section-nav button[data-active=\'true\']')
    expect(styles).toContain('background: var(--blue)')
    expect(styles).toContain('.state-history-chart-card.tone-green')
    expect(styles).toContain('.server-flag .fi')
    expect(styles).toContain('.latency-chart-tooltip rect')
    expect(styles).not.toContain('<title>')
    expect(styles).not.toContain('.state-history-chart-card::before')
    expect(styles).not.toContain('--zeno-accent-gradient')
  })
})

describe('visual weight polish', () => {
  it('avoids heavy UI font weights in the Nezha-like shell', () => {
    expect(styles).not.toContain('font-weight: 650')
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
