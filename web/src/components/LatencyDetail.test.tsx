import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import type { HomeCardNode, StatePoint } from '../types'
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
    uptimeSeconds: 3660,
  },
]

describe('LatencyDetail', () => {
  it('includes the agent state history panel without removing the latency monitor', () => {
    const html = renderToStaticMarkup(
      <LatencyDetail
        node={node}
        points={[]}
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
    expect(html).toContain('运行 1 小时 1 分钟')
    expect(html).toContain('负载 0.42 / 0.35 / 0.28')
    expect(html).toContain('debian 13')
    expect(html).toContain('6.12.0')
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
    expect(html).toContain('实时 · 1 个状态采样')
    expect(html).toContain('Hytron 网络延迟')
    expect(html).toContain('monitor services')
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
    expect(html).toContain('实时')
    expect(html).toContain('1 天')
    expect(html).toContain('7 天')
    expect(html).toContain('30 天')
    expect(html).toContain('削峰')
  })
})
