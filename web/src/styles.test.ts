// @ts-nocheck
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { describe, expect, it } from 'vitest'

const stylesPath = join(dirname(fileURLToPath(import.meta.url)), 'styles.css')
const styles = readFileSync(stylesPath, 'utf8')

describe('mobile latency target layout', () => {
  it('keeps latency target buttons readable as card tiles on phones', () => {
    expect(styles).toContain('@media (max-width: 767px)')
    expect(styles).toContain('.latency-target-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 6px; padding: 0 12px 12px; }')
    expect(styles).toContain('.latency-target-grid button')
    expect(styles).toContain('border-radius: var(--radius-field)')
    expect(styles).toContain('.latency-target-grid.is-loading button')
    expect(styles).toContain('.latency-panel-skeleton')
    expect(styles).toContain('.resource-chart-frame')
    expect(styles).toContain('grid-template-columns: 46px minmax(0, 1fr)')
    expect(styles).toContain('.latency-chart .axis-label { font-size: 10px; }')
  })
})

describe('mobile server detail layout', () => {
  it('stacks dense desktop detail facts into readable mobile rows', () => {
    expect(styles).toContain('@media (max-width: 767px)')
    expect(styles).toContain('.detail-hero__main { align-items: center; flex-direction: row; gap: 10px; }')
    expect(styles).toContain('.detail-fact-strip { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 6px; }')
    expect(styles).toContain('.detail-fact.is-wide { grid-column: 1 / -1; min-height: 54px; padding: 8px 9px; }')
    expect(styles).toContain('.detail-fact:not(.is-wide) strong { overflow: visible; text-overflow: clip; white-space: normal; overflow-wrap: anywhere; }')
    expect(styles).toContain('.monitor-heading { padding: 16px 14px 10px; flex-direction: row; align-items: flex-start; gap: 8px; }')
    expect(styles).toContain('.monitor-heading-actions { width: auto; flex: none; flex-wrap: nowrap; align-items: flex-start; justify-content: flex-end; margin-left: auto; }')
    expect(styles).toContain('.resource-history-header { padding: 16px 14px 10px; flex-direction: row; align-items: flex-start; gap: 8px; }')
    expect(styles).toContain('.resource-range-row { width: auto; flex: none; flex-wrap: nowrap; justify-content: flex-end; margin-left: auto; }')
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
    expect(styles).toContain('.home-summary__metric')
    expect(styles).toContain('grid-template-columns: repeat(5, minmax(0, 1fr))')
    expect(styles).toContain('--summary-accent')
    expect(styles).toContain('.home-summary__metric--send')
    expect(styles).toContain('.home-summary__metric--rate dd { width: 13.5ch; min-width: 13.5ch; justify-self: start; justify-content: flex-start; font-weight: 600; }')
    expect(styles).toContain('.home-summary__rate-value')
    expect(styles).toContain('font-weight: 400')
    expect(styles).not.toContain('.home-summary__metric::after')
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

  it('polishes backend mobile navigation, modal actions, and save buttons without overlap', () => {
    expect(styles).toContain('@media (max-width: 767px)')
    expect(styles).toContain('.admin-section-nav {')
    expect(styles).toContain('grid-template-columns: repeat(auto-fit, minmax(96px, 1fr))')
    expect(styles).toContain('overflow: visible')
    expect(styles).toContain('.admin-section-nav button { min-width: 0; min-height: 40px; padding: 0 8px; border-radius: var(--radius-field); }')
    expect(styles).not.toContain('scroll-snap-type: x proximity')
    expect(styles).toContain('.admin-modal { width: 100%; height: min(92dvh, 760px); max-height: calc(100dvh - 16px); border-radius: var(--radius-panel); padding: 0; }')
    expect(styles).toContain('.admin-modal-actions {')
    expect(styles).toContain('position: sticky')
    expect(styles).toContain('bottom: 0')
    expect(styles).toContain('max(10px, env(safe-area-inset-bottom))')
    expect(styles).toContain('.admin-settings-form > button[type="submit"] { width: 100%; margin-top: 14px; }')
    expect(styles).toContain('.admin-alert-rule-add-form .admin-rule-picker { margin-top: 0; }')
    expect(styles).toContain('.admin-node-edit-form button.admin-primary-action')
    expect(styles).toContain('.admin-section-heading h3 { white-space: nowrap; font-size: 22px; }')
    expect(styles).toContain('.admin-section-actions { width: auto; flex: 0 0 auto; margin-left: auto; justify-content: flex-end; flex-wrap: nowrap; gap: 6px; }')
    expect(styles).toContain('.admin-block-heading .admin-primary-action { height: 34px; padding: 0 10px; font-size: 12px; }')
    expect(styles).toContain('.admin-target-assignment-list,')
    expect(styles).toContain('.admin-notification-list .admin-row-actions.admin-icon-actions,')
  })

  it('keeps authenticated node management in compact lists without old card shells', () => {
    expect(styles).toContain('.admin-login-card')
    expect(styles).toContain('width: min(100%, 420px)')
    expect(styles).toContain('grid-template-columns: 1fr')
    expect(styles).toContain('.admin-login-title { text-align: center; }')
    expect(styles).not.toContain('grid-template-columns: minmax(0, 1fr) minmax(140px, 220px)')
    expect(styles).toContain('.admin-node-section')
    expect(styles).toContain(".admin-section-nav button[data-active='true']")
    expect(styles).toContain('.admin-node-status')
    expect(styles).toContain('.admin-status-indicator.status-online .admin-status-dot')
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
    expect(styles).toContain('.resource-history-grid')
    expect(styles).toContain('grid-template-columns: repeat(auto-fit, minmax(290px, 1fr))')
    expect(styles).toContain('grid-template-columns: 1fr')
    expect(styles).toContain('.resource-card')
    expect(styles).toContain('background: var(--card)')
    expect(styles).toContain('.resource-y-axis')
    expect(styles).toContain('.resource-chart-plot')
    expect(styles).toContain('.resource-x-axis')
    expect(styles).toContain('.resource-chart-area { opacity: .2; stroke: none; pointer-events: none; }')
    expect(styles).toContain('.resource-card-header')
    expect(styles).toContain('grid-template-columns: minmax(0, 1fr) auto')
  })

  it('keeps only the server status as a compact pill in the top server card', () => {
    expect(styles).toContain('.detail-hero__badges')
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
    expect(styles).toContain('.usage-track { position: relative; width: 100%; height: 10px; overflow: hidden; border: 0;')
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

  it('uses solid accent colors without gradients and keeps key identity emphasized', () => {
    expect(styles).toContain('.home-summary__status-line strong')
    expect(styles).toContain('.home-summary__metrics dd')
    expect(styles).toContain('.metric-label { color: var(--metric-accent)')
    expect(styles).toContain('.node-title-line p { min-width: 0; margin: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--foreground); font-size: 13px; font-weight: 600')
    expect(styles).toContain('.detail-title-button span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }')
    expect(styles).toContain('.node-metric strong')
    expect(styles).toContain('.node-metric strong { min-width: 7.5ch; margin-left: 0; overflow: hidden; text-align: right; text-overflow: ellipsis; white-space: nowrap; color: var(--metric-accent); font-weight: 500')
    expect(styles).toContain('.metric-down strong { min-width: 9.5ch; }')
    expect(styles).toContain('.metric-latency { --metric-accent: var(--green); }')
    expect(styles).not.toContain('.metric-latency strong { font-weight: 600; }')
    expect(styles).toContain('.latency-target-grid strong { color: var(--foreground); font-size: 20px')
    expect(styles).toContain('font-weight: 600; letter-spacing: -0.03em')
    expect(styles).toContain('.latency-monitor-heading .monitor-title-row h3 { font-weight: 600; }')
    expect(styles).toContain('.home-summary__status-line strong')
    expect(styles).toContain('font-size: clamp(10.5px, .94vw, 12px)')
    expect(styles.match(/font-weight: 600/g)?.length ?? 0).toBeGreaterThanOrEqual(5)
    expect(styles).toContain('.latency-target-grid em { color: var(--muted)')
    expect(styles).toContain('.admin-section-nav button[data-active=\'true\']')
    expect(styles).toContain('.admin-section-icon')
    expect(styles).toContain('background: var(--blue)')
    expect(styles).toContain('.resource-card.tone-green')
    expect(styles).toContain('.resource-chart-tooltip')
    expect(styles).toContain('.server-flag .fi')
    expect(styles).toContain('.latency-chart-tooltip foreignObject')
    expect(styles).toContain('.latency-tooltip-card')
    expect(styles).not.toContain('<title>')
    expect(styles).not.toContain('.resource-card::before')
    expect(styles).not.toContain('--zeno-accent-gradient')
    expect(styles).not.toContain('linear-gradient')
    expect(styles).not.toContain('radial-gradient')
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
    expect(styles).toContain('.resource-chart-line { fill: none; stroke-width: 1.55')
  })
})
