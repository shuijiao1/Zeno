import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { AdminDashboard, HomeTopPanel } from './App'

const overviewProps = {
  totalCount: 11,
  onlineCount: 9,
  offlineCount: 2,
  totalUp: 1024,
  totalDown: 2048,
  upSpeed: 128,
  downSpeed: 256,
}

describe('HomeTopPanel', () => {
  it('keeps every homepage top control inside one card above server cards', () => {
    const html = renderToStaticMarkup(
      <HomeTopPanel
        {...overviewProps}
        onHome={() => {}}
        onAdmin={() => {}}
      />,
    )

    expect(html).toContain('home-top-card')
    expect(html).toContain('dashboard actions')
    expect(html).toContain('水饺的探针')
    expect(html).toContain('服务器总数')
    expect(html).toContain('在线服务器')
    expect(html).toContain('离线服务器')
    expect(html).toContain('网络')
    expect(html.match(/overview-metric/g)).toHaveLength(4)
    expect(html.indexOf('水饺的探针')).toBeLessThan(html.indexOf('服务器总数'))
    expect(html).not.toContain('overview-card--combined')
  })
})

describe('AdminDashboard', () => {
  it('uses the same card shell and action style as the public front page', () => {
    const html = renderToStaticMarkup(<AdminDashboard onHome={() => {}} />)

    expect(html).toContain('home-top-card')
    expect(html).toContain('admin-panel')
    expect(html).toContain('JiaoProbe 后台')
    expect(html).toContain('沿用前台卡片风格')
    expect(html).toContain('dashboard actions')
  })

  it('renders authenticated admin nodes without rendering the admin token', () => {
    const html = renderToStaticMarkup(
      <AdminDashboard
        onHome={() => {}}
        hasAdminToken
        adminState={{
          kind: 'ready',
          nodes: [
            {
              id: 'hytron',
              displayName: 'Hytron',
              status: 'online',
              countryCode: 'HK',
              region: 'Hong Kong',
              disabled: false,
              billingMode: 'both',
              monthlyQuotaBytes: 1099511627776,
              lastSeenAt: '2026-07-03T00:00:00Z',
              createdAt: '2026-07-02T00:00:00Z',
              updatedAt: '2026-07-03T00:00:00Z',
              hostname: 'hytron-real',
              osName: 'debian',
              osVersion: '13',
              kernel: '6.12.0',
              arch: 'x86_64',
              virtualization: 'kvm',
              cpuModel: 'AMD EPYC',
              cpuCores: 2,
              memoryTotalBytes: 2147483648,
              diskTotalBytes: 42949672960,
              bootTime: '2026-07-02T01:00:00Z',
              agentVersion: 'agent-test',
            },
          ],
        }}
        onAdminTokenSubmit={() => {}}
        onAdminTokenClear={() => {}}
        onAdminRefresh={() => {}}
      />,
    )

    expect(html).toContain('节点列表')
    expect(html).toContain('Hytron')
    expect(html).toContain('online')
    expect(html).toContain('agent-test')
    expect(html).toContain('debian 13')
    expect(html).not.toContain('admin-pass')
  })
})
