import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import type { HomeCardNode } from '../types'
import { ServerCard } from './ServerCard'

const baseNode: HomeCardNode = {
  id: 'example-node-a',
  displayName: 'Example Node A',
  status: 'online',
  os: 'debian',
  osVersion: '13',
  kernel: '6.12.0',
  arch: 'x86_64',
  cpuModel: 'AMD EPYC',
  countryCode: 'HK',
  cpuCores: 2,
  expiryLabel: '永 久',
  cpuPercent: 12.5,
  load1: 0.42,
  load5: 0.35,
  load15: 0.28,
  memoryUsedBytes: 1024,
  memoryTotalBytes: 4096,
  diskUsedBytes: 2048,
  diskTotalBytes: 8192,
  netInSpeedBps: 128,
  netOutSpeedBps: 256,
  netInTotalBytes: 1024,
  netOutTotalBytes: 2048,
  monthlyBillableBytes: 1024,
  monthlyQuotaBytes: 4096,
}

describe('ServerCard', () => {
  it('renders non-online nodes as frozen metric cards without an offline text watermark', () => {
    const html = renderToStaticMarkup(
      <ServerCard node={{ ...baseNode, status: 'warning' }} onOpen={vi.fn()} />,
    )

    expect(html).toContain('kulin-node-card is-offline')
    expect(html).toContain('node-head')
    expect(html).toContain('node-specs')
    expect(html).toContain('node-usage')
    expect(html).toContain('<p>Example Node A</p>')
    expect(html).toContain('node-dot status-offline')
    expect(html).not.toContain('node-offline-watermark')
    expect(html).not.toContain('离线')
    expect(html).not.toContain('node-offline-state')
  })

  it('keeps online nodes as normal metric cards', () => {
    const html = renderToStaticMarkup(<ServerCard node={{ ...baseNode, monthlyPeriodStart: '2026-07-01', monthlyPeriodEnd: '2026-07-31', monthlyResetDay: 1 }} onOpen={vi.fn()} />)

    expect(html).toContain('node-head')
    expect(html).toContain('node-specs')
    expect(html).toContain('node-usage')
    expect(html).toContain('Example Node A')
    expect(html).toContain('永久')
    expect(html).toContain('>流量</span>')
    expect(html).not.toContain('>负载</span>')
    expect(html).not.toContain('0.42 / 0.35 / 0.28')
    expect(html).not.toContain('流量 ·')
    expect(html).not.toContain('7/1')
    expect(html).not.toContain('每月 1 日重置')
    expect(html).not.toContain('node-offline-watermark')
    expect(html).not.toContain('node-offline-state')
  })

  it('hides the expiry badge when a node has no date and is not marked permanent', () => {
    const html = renderToStaticMarkup(<ServerCard node={{ ...baseNode, expiryLabel: '' }} onOpen={vi.fn()} />)

    expect(html).not.toContain('node-expiry')
    expect(html).not.toContain('永久')
  })

  it('styles precomputed recurring expiry labels by urgency', () => {
    const html = renderToStaticMarkup(<ServerCard node={{ ...baseNode, expiryLabel: '余 3 天' }} onOpen={vi.fn()} />)

    expect(html).toContain('node-expiry is-urgent')
    expect(html).toContain('余 3 天')
  })
})
