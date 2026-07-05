import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { StatePoint } from '../types'
import { StateHistoryPanel } from './StateHistoryPanel'

const points: StatePoint[] = [
  {
    ts: '2026-07-02T12:00:00Z',
    cpuPercent: 12.5,
    load1: 0.22,
    load5: 0.18,
    load15: 0.12,
    memoryUsedBytes: 1024,
    memoryTotalBytes: 4096,
    swapUsedBytes: 256,
    swapTotalBytes: 2048,
    diskUsedBytes: 2048,
    diskTotalBytes: 8192,
    netInTotalBytes: 10 * 1024,
    netOutTotalBytes: 20 * 1024,
    netInSpeedBps: 128 * 1024,
    netOutSpeedBps: 256 * 1024,
    processCount: 80,
    tcpConnectionCount: 28,
    uptimeSeconds: 3600,
  },
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

describe('StateHistoryPanel', () => {
  it('renders real agent state history metrics and sparklines', () => {
    const html = renderToStaticMarkup(
      <StateHistoryPanel points={points} range="1h" loading={false} />,
    )

    expect(html).toContain('系统资源历史')
    expect(html).toContain('实时 · 2 个状态采样')
    expect(html).toContain('resource history range selector')
    expect(html).toContain('实时')
    expect(html).toContain('1 天')
    expect(html).toContain('7 天')
    expect(html).toContain('30 天')
    expect(html).not.toContain('运行 1 小时 1 分钟')
    expect(html).not.toContain('温度')
    expect(html).toContain('state-history-stack')
    expect(html).toContain('state-history-chart-card')
    expect(html).toContain('viewBox="0 0 900 180"')
    expect(html).toContain('CPU')
    expect(html).toContain('18.8%')
    expect(html).toContain('内存')
    expect(html).toContain('50.0%')
    expect(html).toContain('磁盘')
    expect(html).toContain('50.0%')
    expect(html).toContain('网络速率')
    expect(html).toContain('↑512.0 KiB/s')
    expect(html).toContain('↓64.0 KiB/s')
    expect(html).toContain('系统负载')
    expect(html).toContain('进程 / TCP')
    expect(html).toContain('网络累计')
    expect(html).toContain('↑24.00 KB')
    expect(html).toContain('↓12.00 KB')
    expect(html).toContain('data-series="cpu"')
    expect(html).toContain('data-series="memory"')
    expect(html).toContain('data-series="disk"')
    expect(html).toContain('data-series="net-out"')
    expect(html).toContain('data-series="net-in"')
    expect(html).toContain('data-series="load1"')
    expect(html).toContain('data-series="processes"')
    expect(html).toContain('data-series="tcp"')
    expect(html).toContain('data-series="net-out-total"')
  })

  it('shows an explicit empty state instead of a blank chart', () => {
    const html = renderToStaticMarkup(
      <StateHistoryPanel points={[]} range="1d" loading={false} />,
    )

    expect(html).toContain('暂无系统资源历史')
  })
})
