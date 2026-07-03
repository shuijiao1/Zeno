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

  it('renders a unified username and password login screen when unauthenticated', () => {
    const html = renderToStaticMarkup(<AdminDashboard onHome={() => {}} />)

    expect(html).toContain('admin-login-card')
    expect(html).toContain('name="admin-username"')
    expect(html).toContain('name="admin-password"')
    expect(html).toContain('placeholder="admin"')
    expect(html).toContain('默认账号：admin / admin')
    expect(html).not.toContain('Admin Token')
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
          targets: [],
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

  it('renders inline node edit controls for authenticated node management', () => {
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
              createdAt: '2026-07-02T00:00:00Z',
              updatedAt: '2026-07-03T00:00:00Z',
              cpuCores: 2,
              memoryTotalBytes: 2147483648,
              diskTotalBytes: 42949672960,
            },
          ],
          targets: [],
        }}
        onAdminTokenSubmit={() => {}}
        onAdminTokenClear={() => {}}
        onAdminRefresh={() => {}}
        onAdminNodeUpdate={() => {}}
      />,
    )

    expect(html).toContain('admin-node-edit-form')
    expect(html).toContain('name="display-name"')
    expect(html).toContain('name="country-code"')
    expect(html).toContain('name="region"')
    expect(html).toContain('name="monthly-quota-gb"')
    expect(html).toContain('禁用节点')
    expect(html).toContain('保存节点')
    expect(html).not.toContain('admin-pass')
  })

  it('renders backend-first node create controls and install command action in node edit cards', () => {
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
              disabled: false,
              billingMode: 'both',
              monthlyQuotaBytes: null,
              createdAt: '2026-07-02T00:00:00Z',
              updatedAt: '2026-07-03T00:00:00Z',
              cpuCores: null,
              memoryTotalBytes: null,
              diskTotalBytes: null,
            },
          ],
          targets: [],
        }}
        onAdminNodeCreate={() => {}}
        onAdminInstallCommand={async () => 'install command'}
      />,
    )

    expect(html).toContain('admin-node-create-form')
    expect(html).toContain('添加服务器')
    expect(html).toContain('name="new-display-name"')
    expect(html).toContain('获取安装命令')
    expect(html).not.toContain('admin-pass')
  })

  it('renders authenticated probe target inventory in the admin shell', () => {
    const html = renderToStaticMarkup(
      <AdminDashboard
        onHome={() => {}}
        hasAdminToken
        adminState={{
          kind: 'ready',
          nodes: [],
          targets: [
            {
              id: 'hytron-local',
              name: 'Hytron',
              type: 'tcping',
              address: '127.0.0.1',
              port: 18980,
              count: 3,
              timeoutMs: 1200,
              intervalSec: 60,
              enabled: true,
              assignments: [
                { nodeId: 'hytron', nodeDisplayName: 'Hytron', enabled: true },
              ],
            },
          ],
        }}
        onAdminTokenSubmit={() => {}}
        onAdminTokenClear={() => {}}
        onAdminRefresh={() => {}}
      />,
    )

    expect(html).toContain('探针目标')
    expect(html).toContain('admin-target-card')
    expect(html).toContain('hytron-local')
    expect(html).toContain('127.0.0.1:18980')
    expect(html).toContain('3 次 / 1200ms / 60s')
    expect(html).toContain('Hytron')
    expect(html).not.toContain('admin-pass')
  })

  it('renders probe target create and edit controls without leaking admin token', () => {
    const html = renderToStaticMarkup(
      <AdminDashboard
        onHome={() => {}}
        hasAdminToken
        adminState={{
          kind: 'ready',
          nodes: [],
          targets: [
            {
              id: 'hytron-local',
              name: 'Hytron',
              type: 'tcping',
              address: '127.0.0.1',
              port: 18980,
              count: 3,
              timeoutMs: 1200,
              intervalSec: 60,
              enabled: true,
              assignments: [],
            },
          ],
        }}
        onAdminProbeTargetCreate={() => {}}
        onAdminProbeTargetUpdate={() => {}}
      />,
    )

    expect(html).toContain('admin-target-create-form')
    expect(html).toContain('添加目标')
    expect(html).toContain('admin-target-edit-form')
    expect(html).toContain('name="target-name"')
    expect(html).toContain('name="target-address"')
    expect(html).toContain('name="target-port"')
    expect(html).toContain('保存目标')
    expect(html).not.toContain('admin-pass')
  })
})
