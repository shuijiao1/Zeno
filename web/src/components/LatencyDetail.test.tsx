import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import type { HomeCardNode, LatencyPoint, StatePoint } from '../types'
import { LatencyDetail } from './LatencyDetail'

const node = {
  id: 'hytron',
  displayName: 'Hytron',
  status: 'online',
  os: 'debian',
  arch: 'aarch64',
  osVersion: '13',
  kernel: '6.12.0',
  virtualization: 'kvm',
  cpuModel: 'AMD EPYC 7B13',
  countryCode: 'HK',
  bootTime: '2026-07-02T01:00:00Z',
  cpuCores: 2,
  cpuPercent: 12,
  memoryUsedBytes: 1024,
  memoryTotalBytes: 4096,
  diskUsedBytes: 2048,
  diskTotalBytes: 8192,
  load1: 0.11,
  load5: 0.12,
  load15: 0.13,
  uptimeSeconds: 120,
  netInSpeedBps: 128,
  netOutSpeedBps: 256,
  netInTotalBytes: 1024,
  netOutTotalBytes: 2048,
  monthlyBillableBytes: 3072,
  monthlyQuotaBytes: null,
} as HomeCardNode

const statePoints: StatePoint[] = [
  {
    ts: '2026-07-02T12:01:00Z',
    cpuPercent: 18.75,
    load1: 0.42,
    load5: 0.35,
    load15: 0.28,
    memoryUsedBytes: 2048,
    memoryTotalBytes: 4096,
    swapUsedBytes: 512,
    swapTotalBytes: 2048,
    diskUsedBytes: 4096,
    diskTotalBytes: 8192,
    netInTotalBytes: 12 * 1024,
    netOutTotalBytes: 24 * 1024,
    netInSpeedBps: 64 * 1024,
    netOutSpeedBps: 512 * 1024,
    processCount: 88,
    tcpConnectionCount: 34,
    udpConnectionCount: 12,
    uptimeSeconds: 3660,
  },
]

const latencyPoints: LatencyPoint[] = [
  { ts: '2026-07-02T12:01:00Z', targetId: 'telegram', targetName: 'Telegram', medianMs: 32.5, avgMs: 33.1, lossPercent: 0 },
]

describe('LatencyDetail', () => {
  it('includes the agent state history panel without removing the latency monitor', () => {
    const html = renderToStaticMarkup(
      <LatencyDetail
        node={node}
        points={latencyPoints}
        statePoints={statePoints}
        stateLoading={false}
        range="1d"
        onBack={vi.fn()}
        onRangeChange={vi.fn()}
      />,
    )

    expect(html).toContain('detail-hero')
    expect(html).toContain('detail-status-pill')
    expect(html).toContain('detail-fact-strip')
    expect(html).toContain('Hytron')
    expect(html).toContain('AMD EPYC 7B13')
    expect(html).toContain('2 Cores')
    expect(html).not.toContain('kvm')
    expect(html).not.toContain('Standard PC')
    expect(html).toContain('运行时间')
    expect(html).toContain('1 小时 1 分钟')
    expect(html).toContain('负载')
    expect(html).toContain('0.42 / 0.35 / 0.28')
    expect(html).toContain('debian 13')
    expect(html).toContain('6.12.0')
    expect(html).not.toContain('debian 13 · aarch64 · 6.12.0 · HK')
    expect(html).not.toContain('12.0% · AMD EPYC 7B13')
    expect(html).not.toContain('1.00 KB / 4.00 KB')
    expect(html).not.toContain('2.00 KB / 8.00 KB')
    expect(html).toContain('18.8%')
    expect(html).not.toContain('CPU 使用')
    expect(html).not.toContain('规格')
    expect(html).toContain('内存')
    expect(html).toContain('磁盘')
    expect(html).toContain('开机时间')
    expect(html).toContain('2026/7/2 09:00:00')
    expect(html).not.toContain('↑256 B/s ↓128 B/s')
    expect(html).not.toContain('detail-info-card')
    expect(html).toContain('系统资源历史')
    expect(html).not.toContain('实时 · 1 个状态采样')
    expect(html).not.toContain('个状态采样')
    expect(html).not.toContain('Hytron 网络延迟')
    expect(html).not.toContain('1 天 · 0 个监控服务')
    expect(html).toContain('monitor services')
  })

  it('keeps detail facts and latency area reserved while live data is loading', () => {
    const html = renderToStaticMarkup(
      <LatencyDetail
        node={node}
        points={[]}
        loading
        statePoints={[]}
        stateLoading
        range="1d"
        onBack={vi.fn()}
        onRangeChange={vi.fn()}
      />,
    )

    expect(html).toContain('运行时间')
    expect(html).toContain('负载')
    expect(html).toContain('2 分钟')
    expect(html).toContain('0.11 / 0.12 / 0.13')
    expect(html).toContain('累计流量')
    expect(html).toContain('latency-target-grid is-loading')
    expect(html).toContain('latency-panel-skeleton')
    expect(html).not.toContain('正在读取网络延迟')
  })

  it('maps non-online detail status to offline', () => {
    const html = renderToStaticMarkup(
      <LatencyDetail
        node={{ ...node, status: 'warning' }}
        points={[]}
        range="1d"
        onBack={vi.fn()}
        onRangeChange={vi.fn()}
      />,
    )

    expect(html).toContain('detail-status-pill status-offline')
    expect(html).toContain('离线')
    expect(html).not.toContain('异常')
  })

  it('includes range controls with the latency chart actions', () => {
    const html = renderToStaticMarkup(
      <LatencyDetail
        node={node}
        points={[]}
        range="1d"
        onBack={vi.fn()}
        onRangeChange={vi.fn()}
      />,
    )

    expect(html).toContain('monitor-heading-actions')
    expect(html).toContain('monitor-title-row')
    const latencyRangeMarkup = html.match(/aria-label="latency range selector"[\s\S]*?<\/div>/)?.[0] ?? ''
    expect(latencyRangeMarkup).not.toContain('实时')
    expect(latencyRangeMarkup).toContain('1 天')
    expect(html).toContain('7 天')
    expect(html).toContain('30 天')
    expect(html).toContain('平')
    expect(html).not.toContain('削峰')
  })
})
