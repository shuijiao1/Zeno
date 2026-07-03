import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { AdminDashboard, HomeTopPanel } from './App'
import type { AdminNode, AdminProbeTarget } from './types'

const overviewProps = {
  totalCount: 11,
  onlineCount: 9,
  offlineCount: 2,
  totalUp: 1024,
  totalDown: 2048,
  upSpeed: 128,
  downSpeed: 256,
}

const hytronNode: AdminNode = {
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
}

const backupNode: AdminNode = {
  ...hytronNode,
  id: 'backup',
  displayName: 'Backup',
  status: 'no_data',
  countryCode: undefined,
  region: undefined,
  agentVersion: undefined,
}

const hytronTarget: AdminProbeTarget = {
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
    { nodeId: 'backup', nodeDisplayName: 'Backup', enabled: false },
  ],
}

function renderAdmin(section: 'overview' | 'nodes' | 'targets' | 'notifications' = 'overview') {
  return renderToStaticMarkup(
    <AdminDashboard
      onHome={() => {}}
      hasAdminToken
      initialSection={section}
      adminState={{
        kind: 'ready',
        nodes: [hytronNode, backupNode],
        targets: [hytronTarget],
      }}
      onAdminTokenSubmit={() => {}}
      onAdminTokenClear={() => {}}
      onAdminRefresh={() => {}}
      onAdminNodeCreate={() => {}}
      onAdminNodeUpdate={() => {}}
      onAdminInstallCommand={async () => 'install command'}
      onAdminProbeTargetCreate={() => {}}
      onAdminProbeTargetUpdate={() => {}}
    />,
  )
}

describe('HomeTopPanel', () => {
  it('keeps the homepage top controls inside one card with a simple custom overview', () => {
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
    expect(html).toContain('home-summary')
    expect(html).toContain('home-summary__compact')
    expect(html).toContain('JiaoProbe Overview')
    expect(html).toContain('服务器运行概览')
    expect(html).toContain('累计上传')
    expect(html).toContain('累计下载')
    expect(html).toContain('实时上传')
    expect(html).toContain('实时下载')
    expect(html).not.toContain('服务器总数')
    expect(html).not.toContain('在线服务器')
    expect(html).not.toContain('离线服务器')
    expect(html.indexOf('水饺的探针')).toBeLessThan(html.indexOf('服务器运行概览'))
    expect(html).not.toContain('overview-card--combined')
    expect(html).not.toContain('overview-metric')
  })

  it('treats no-data nodes as not online in the summary copy', () => {
    const html = renderToStaticMarkup(
      <HomeTopPanel
        {...overviewProps}
        onlineCount={0}
        offlineCount={0}
        onHome={() => {}}
        onAdmin={() => {}}
      />,
    )

    expect(html).toContain('/ 11 在线')
    expect(html).toContain('11 台未在线')
    expect(html).not.toContain('全部在线')
  })
})

describe('AdminDashboard', () => {
  it('uses the same card shell and introduces separated backend navigation', () => {
    const html = renderAdmin()

    expect(html).toContain('home-top-card')
    expect(html).toContain('admin-panel')
    expect(html).toContain('JiaoProbe 后台')
    expect(html).toContain('列表只保留关键字段')
    expect(html).toContain('admin-section-nav')
    expect(html).toContain('后台导航')
    expect(html).toContain('概览')
    expect(html).toContain('服务器')
    expect(html).toContain('延迟监控')
    expect(html).toContain('通知')
  })

  it('names notifications as channels and types instead of alerts', () => {
    const html = renderAdmin('notifications')

    expect(html).toContain('通知渠道')
    expect(html).toContain('通知类型')
    expect(html).not.toContain('告警')
  })

  it('renders a unified username and password login screen when unauthenticated', () => {
    const html = renderToStaticMarkup(<AdminDashboard onHome={() => {}} />)

    expect(html).toContain('admin-login-card')
    expect(html).toContain('name="admin-username"')
    expect(html).toContain('name="admin-password"')
    expect(html).toContain('placeholder="admin"')
    expect(html).toContain('默认账号：admin / admin')
    expect(html).toContain('列表 / 弹窗编辑')
    expect(html).not.toContain('Admin Token')
  })

  it('renders authenticated server management as a compact list, not detailed cards', () => {
    const html = renderAdmin('nodes')

    expect(html).toContain('服务器列表')
    expect(html).toContain('admin-list')
    expect(html).toContain('Hytron')
    expect(html).toContain('online')
    expect(html).toContain('agent-test')
    expect(html).toContain('debian 13')
    expect(html).toContain('编辑服务器')
    expect(html).not.toContain('admin-node-card')
    expect(html).not.toContain('name="display-name"')
    expect(html).not.toContain('保存服务器')
    expect(html).not.toContain('admin-pass')
  })

  it('keeps latency monitor management on its own list page', () => {
    const html = renderAdmin('targets')

    expect(html).toContain('延迟监控')
    expect(html).toContain('admin-target-list')
    expect(html).toContain('hytron-local')
    expect(html).toContain('127.0.0.1:18980')
    expect(html).toContain('3 次 / 1200ms / 60s')
    expect(html).toContain('1 / 2 节点启用')
    expect(html).toContain('编辑目标')
    expect(html).not.toContain('admin-target-card')
    expect(html).not.toContain('name="target-name"')
    expect(html).not.toContain('保存目标')
    expect(html).not.toContain('admin-pass')
  })

  it('does not render every admin workspace on one page', () => {
    const nodeHtml = renderAdmin('nodes')
    const targetHtml = renderAdmin('targets')

    expect(nodeHtml).toContain('服务器列表')
    expect(nodeHtml).not.toContain('延迟监控目标列表')
    expect(targetHtml).toContain('延迟监控目标列表')
    expect(targetHtml).not.toContain('服务器列表')
  })
})
