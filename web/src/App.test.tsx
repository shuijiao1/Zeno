import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { AdminDashboard, HomeTopPanel, shellStyleForSettings, validateAdminSettingsInput } from './App'
import type { AdminAlertRule, AdminNode, AdminNotificationChannel, AdminProbeTarget, AdminSettings } from './types'

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
  monthlyResetDay: 1,
  expiryDate: '2026-08-01',
  billingCycle: '月付',
  displayOrder: 10,
  publicIPv4: '198.51.100.8',
  publicIPv6: '2001:db8::8',
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
  displayOrder: 20,
  enabled: true,
  assignments: [
    { nodeId: 'hytron', nodeDisplayName: 'Hytron', enabled: true },
    { nodeId: 'backup', nodeDisplayName: 'Backup', enabled: false },
  ],
}

const pingTarget: AdminProbeTarget = {
  id: 'google-icmp',
  name: 'Example ICMP',
  type: 'ping',
  address: '8.8.8.8',
  port: null,
  count: 4,
  timeoutMs: 900,
  intervalSec: 45,
  displayOrder: 10,
  enabled: true,
  assignments: [
    { nodeId: 'hytron', nodeDisplayName: 'Hytron', enabled: true },
  ],
}

const httpTarget: AdminProbeTarget = {
  id: 'zeno-health-http',
  name: 'Zeno Health HTTP',
  type: 'http_get',
  address: 'https://example.com/health',
  port: null,
  count: 2,
  timeoutMs: 1500,
  intervalSec: 60,
  displayOrder: 30,
  enabled: true,
  assignments: [
    { nodeId: 'hytron', nodeDisplayName: 'Hytron', enabled: true },
  ],
}

const telegramChannel: AdminNotificationChannel = {
  id: 'zeno-telegram',
  name: 'Zeno Telegram',
  destination: '7579942307',
  credentialSet: true,
  enabled: true,
  createdAt: '2026-07-03T00:00:00Z',
  updatedAt: '2026-07-03T00:00:00Z',
}

const alertRules: AdminAlertRule[] = [
  {
    id: 'cpu_high',
    name: 'CPU 使用率',
    category: 'resource',
    metric: 'cpu_percent',
    comparator: '>=',
    threshold: 90,
    thresholdUnit: '%',
    durationSec: 300,
    enabled: true,
    notificationEventType: 'probe_unhealthy',
    notificationLabel: '异常',
    description: 'CPU 使用率持续超过阈值时进入异常通知类型。',
    scopeNodeIds: [],
    createdAt: '2026-07-03T00:00:00Z',
    updatedAt: '2026-07-03T00:00:00Z',
  },
  {
    id: 'node_offline',
    name: '离线判定',
    category: 'liveness',
    metric: 'heartbeat_age_sec',
    comparator: '>=',
    threshold: 180,
    thresholdUnit: 's',
    durationSec: 180,
    enabled: true,
    notificationEventType: 'node_offline',
    notificationLabel: '离线',
    description: 'Agent 心跳超过离线窗口后映射为离线通知类型。',
    scopeNodeIds: ['backup'],
    createdAt: '2026-07-03T00:00:00Z',
    updatedAt: '2026-07-03T00:00:00Z',
  },
]

const settings: AdminSettings = {
  siteTitle: '水饺监控',
  siteSubtitle: 'VPS 状态总览',
  logoUrl: '/assets/logo/custom.png',
  theme: 'dark',
  agentControllerUrl: 'https://zeno.example.com',
  backgroundUrl: 'https://example.com/desktop-bg.webp',
  desktopBackgroundUrl: 'https://example.com/desktop-bg.webp',
  mobileBackgroundUrl: 'https://example.com/mobile-bg.webp',
  updatedAt: '2026-07-04T12:00:00Z',
}

function renderAdmin(section: 'nodes' | 'targets' | 'notifications' | 'account' | 'settings' = 'nodes') {
  return renderToStaticMarkup(
    <AdminDashboard
      onHome={() => {}}
      settings={settings}
      hasAdminToken
      initialSection={section}
      adminState={{
        kind: 'ready',
        account: { username: 'admin' },
        nodes: [hytronNode, backupNode],
        targets: [hytronTarget, pingTarget, httpTarget],
        notificationChannels: [telegramChannel],
        alertRules,
      }}
      onAdminLogin={() => {}}
      onAdminTokenClear={() => {}}
      onAdminAccountUpdate={async () => {}}
      onAdminRefresh={() => {}}
      onAdminNodeCreate={() => {}}
      onAdminNodeUpdate={() => {}}
      onAdminInstallCommand={async () => 'install command'}
      onAdminProbeTargetCreate={() => {}}
      onAdminProbeTargetUpdate={() => {}}
      onAdminNotificationChannelCreate={() => {}}
      onAdminNotificationChannelUpdate={() => {}}
      onAdminAlertRuleUpdate={() => {}}
      onAdminSettingsUpdate={() => {}}
    />,
  )
}

describe('HomeTopPanel', () => {
  it('turns configured desktop and mobile background images into safe shell variables', () => {
    expect(shellStyleForSettings(settings)).toEqual({
      '--zeno-desktop-background-image': 'linear-gradient(rgba(24, 21, 18, 0.78), rgba(24, 21, 18, 0.78)), url("https://example.com/desktop-bg.webp")',
      '--zeno-mobile-background-image': 'linear-gradient(rgba(24, 21, 18, 0.78), rgba(24, 21, 18, 0.78)), url("https://example.com/mobile-bg.webp")',
      backgroundSize: 'cover',
      backgroundAttachment: 'fixed',
    })
    expect(shellStyleForSettings({ ...settings, backgroundUrl: '', desktopBackgroundUrl: '', mobileBackgroundUrl: '' })).toBeUndefined()
  })

  it('keeps the homepage top controls inside one card with a simple custom overview', () => {
    const html = renderToStaticMarkup(
      <HomeTopPanel
        {...overviewProps}
        settings={settings}
        onHome={() => {}}
        onAdmin={() => {}}
      />,
    )

    expect(html).toContain('home-top-card')
    expect(html).toContain('dashboard actions')
    expect(html).toContain('Zeno')
    expect(html).toContain('水饺监控')
    expect(html).toContain('/assets/logo/custom.png')
    expect(html).not.toContain('/assets/avatar/custom.webp')
    expect(html).toContain('VPS 状态总览')
    expect(html).toContain('home-summary')
    expect(html).toContain('home-summary__compact')
    expect(html).toContain('Zeno Overview')
    expect(html).toContain('服务器运行概览')
    expect(html).toContain('累计上传')
    expect(html).toContain('累计下载')
    expect(html).toContain('实时上传')
    expect(html).toContain('实时下载')
    expect(html).not.toContain('服务器总数')
    expect(html).not.toContain('在线服务器')
    expect(html).not.toContain('离线服务器')
    expect(html.indexOf('Zeno')).toBeLessThan(html.indexOf('服务器运行概览'))
    expect(html).not.toContain('overview-card--combined')
    expect(html).not.toContain('overview-metric')
    expect(html).not.toContain(['service', 'status', 'panel'].join('-'))
  })

  it('treats no-data nodes as not online in the summary copy', () => {
    const html = renderToStaticMarkup(
      <HomeTopPanel
        {...overviewProps}
        onlineCount={0}
        offlineCount={0}
        settings={settings}
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
  it('uses the same card shell and opens backend directly on the server list', () => {
    const html = renderAdmin()

    expect(html).toContain('home-top-card')
    expect(html).toContain('admin-panel')
    expect(html).not.toContain(['Zeno', '后台'].join(' '))
    expect(html).not.toContain('控' + '制台')
    expect(html).not.toContain('列表只保留' + '关键字段')
    expect(html).toContain('admin-section-nav')
    expect(html).toContain('后台导航')
    expect(html).toContain('服务器')
    expect(html).toContain('延迟监控')
    expect(html).toContain('2 类型')
    expect(html).not.toContain('1 异常 / 2 类型')
    expect(html).toContain('账户')
    expect(html).toContain('设置')
    expect(html).toContain('通知')
    expect(html).not.toContain('修改密码</button>')
    expect(html).toContain('服务器列表')
    expect(html).toContain('Hytron')
    expect(html).not.toContain('admin-overview-panel')
  })

  it('renders account settings as a dedicated account page', () => {
    const html = renderAdmin('account')

    expect(html).toContain('账户')
    expect(html).toContain('修改账号和密码')
    expect(html).toContain('登录信息')
    expect(html).toContain('修改密码')
    expect(html).toContain('name="account-username"')
    expect(html).toContain('value="admin"')
    expect(html).toContain('name="current-password"')
    expect(html).toContain('name="new-password"')
    expect(html).toContain('保存账户')
    expect(html).not.toContain('服务器列表')
  })

  it('renders settings as a lightweight appearance configuration page', () => {
    const html = renderAdmin('settings')

    expect(html).toContain('站点设置')
    expect(html).toContain('外观配置')
    expect(html).toContain('站点信息')
    expect(html).toContain('主题与背景')
    expect(html).toContain('admin-settings-form')
    expect(html).toContain('name="site-title"')
    expect(html).toContain('水饺监控')
    expect(html).toContain('name="site-subtitle"')
    expect(html).toContain('VPS 状态总览')
    expect(html).toContain('name="logo-url"')
    expect(html).toContain('/assets/logo/custom.png')
    expect(html).toContain('头像 / Logo URL')
    expect(html).not.toContain('/assets/avatar/custom.webp')
    expect(html).not.toContain('name="avatar-url"')
    expect(html).toContain('name="theme"')
    expect(html).toContain('深色')
    expect(html).toContain('Agent 接入 URL')
    expect(html).toContain('name="agent-controller-url"')
    expect(html).toContain('https://zeno.example.com')
    expect(html).toContain('图片字段只填 https:// 链接或 /assets/... 站内路径')
    expect(html).toContain('name="desktop-background-url"')
    expect(html).toContain('https://example.com/desktop-bg.webp')
    expect(html).toContain('name="mobile-background-url"')
    expect(html).toContain('https://example.com/mobile-bg.webp')
    expect(html).not.toContain('token')
    expect(html).not.toContain('secret')
    expect(html).not.toContain('credential')
    expect(html).not.toContain('hash')
  })

  it('validates settings URL fields before saving', () => {
    const baseInput = {
      siteTitle: 'Zeno',
      siteSubtitle: '服务器运行概览',
      logoUrl: '/assets/logo/id.png',
      theme: 'system' as const,
      agentControllerUrl: '',
      backgroundUrl: 'https://example.com/desktop.webp',
      desktopBackgroundUrl: 'https://example.com/desktop.webp',
      mobileBackgroundUrl: '',
    }

    expect(validateAdminSettingsInput(baseInput)).toBeNull()
    expect(validateAdminSettingsInput({ ...baseInput, logoUrl: 'http://example.com/logo.png' })).toContain('头像 / Logo URL')
    expect(validateAdminSettingsInput({ ...baseInput, desktopBackgroundUrl: 'javascript:alert(1)' })).toContain('电脑端背景图 URL')
    expect(validateAdminSettingsInput({ ...baseInput, mobileBackgroundUrl: '//example.com/bg.png' })).toContain('手机端背景图 URL')
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'https://user:pass@example.com' })).toContain('Agent 接入 URL')
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'https://zeno.example.com/?token=1' })).toContain('Agent 接入 URL')
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'https://zeno.example.com/' })).toBeNull()
  })

  it('renders real notification channels and types instead of a placeholder', () => {
    const html = renderAdmin('notifications')

    expect(html).toContain('通知渠道')
    expect(html).toContain('通知类型')
    expect(html).toContain('Zeno Telegram')
    expect(html).toContain('7579942307')
    expect(html).toContain('凭据已设置')
    expect(html).toContain('添加通知类型')
    expect(html).toContain('CPU 使用率')
    expect(html).toContain('启用中')
    expect(html).toContain('添加通知渠道')
    expect(html).toContain('编辑渠道')
    expect(html).toContain('删除渠道')
    expect(html).toContain('测试发送')
    expect(html).not.toContain('最近发送')
    expect(html).not.toContain('发送失败')
    expect(html).not.toContain('telegram returned status 500')
    expect(html).not.toContain('后续再接入')
    expect(html).not.toContain('telegram-bot-secret')
    expect(html).not.toContain('告警')
  })

  it('renders notification type triggers without current-hit or delivery history sections', () => {
    const html = renderAdmin('notifications')

    expect(html).toContain('通知类型')
    expect(html).toContain('CPU 使用率')
    expect(html).toContain('cpu_percent')
    expect(html).toContain('&gt;= 90%')
    expect(html).toContain('持续 300s')
    expect(html).toContain('全部服务器')
    expect(html).toContain('Backup (backup)')
    expect(html).toContain('离线判定')
    expect(html).toContain('node_offline')
    expect(html).toContain('添加通知类型')
    expect(html).toContain('编辑通知类型')
    expect(html).toContain('移除')
    expect(html).not.toContain('触发条件</h4>')
    expect(html).not.toContain('当前异常')
    expect(html).not.toContain('当前值 95.25%')
    expect(html).not.toContain('通知：异常')
    expect(html).not.toContain('告警')
    expect(html).not.toContain('telegram-bot-secret')
  })


  it('renders a unified username and password login screen when unauthenticated', () => {
    const html = renderToStaticMarkup(<AdminDashboard onHome={() => {}} />)

    expect(html).toContain('admin-login-card')
    expect(html).toContain('name="admin-username"')
    expect(html).toContain('name="admin-password"')
    expect(html).toContain('placeholder="admin"')
    expect(html).toContain('后台登录')
    expect(html).not.toContain('默认账号：' + 'admin / admin')
    expect(html).not.toContain('列表 / 弹窗编辑')
    expect(html).not.toContain('控' + '制台')
    expect(html).not.toContain('Admin Token')
  })

  it('renders authenticated server management as a compact list, not detailed cards', () => {
    const html = renderAdmin('nodes')

    expect(html).toContain('服务器列表')
    expect(html).toContain('admin-list')
    expect(html).toContain('Hytron')
    expect(html).toContain('>在线<')
    expect(html).toContain('>暂无数据<')
    expect(html).toContain('agent-test')
    expect(html).toContain('198.51.100.8')
    expect(html).toContain('2001:db8::8')
    expect(html).not.toContain('v4 198.51.100.8')
    expect(html).not.toContain('v6 2001:db8::8')
    expect(html).toContain('admin-ip-stack')
    expect(html).not.toContain('debian 13')
    expect(html).not.toContain('2026-08-01')
    expect(html).not.toContain('月付')
    expect(html).not.toContain('sharon · 🇭🇰 HK · 顺序 10')
    expect(html).not.toContain('顺序 10')
    expect(html).toContain('整理顺序')
    expect(html).not.toContain('上移')
    expect(html).not.toContain('下移')
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
    expect(html).toContain('name="target-sort"')
    expect(html).toContain('按手动顺序')
    expect(html).toContain('按名称排序')
    expect(html).toContain('整理顺序')
    expect(html).toContain('>启用中<')
    expect(html).not.toContain('hytron-local')
    expect(html).not.toContain('顺序 20')
    expect(html).toContain('127.0.0.1:18980')
    expect(html).not.toContain('3 次 / 1200ms / 60s')
    expect(html).toContain('1 / 2 节点启用')
    expect(html).toContain('编辑目标')
    expect(html).not.toContain('停用目标')
    expect(html).not.toContain('删除目标')
    expect(html).not.toContain('全节点启用')
    expect(html).not.toContain('全节点停用')
    expect(html).not.toContain('上移')
    expect(html).not.toContain('下移')
    expect(html.indexOf('Example ICMP')).toBeLessThan(html.indexOf('Hytron'))
    expect(html).not.toContain('admin-target-card')
    expect(html).not.toContain('name="target-name"')
    expect(html).not.toContain('保存目标')
    expect(html).not.toContain('admin-pass')
  })

  it('renders ping monitor targets without requiring a port', () => {
    const html = renderAdmin('targets')

    expect(html).toContain('Example ICMP')
    expect(html).not.toContain('ICMP Ping')
    expect(html).toContain('8.8.8.8')
    expect(html).not.toContain('4 次 / 900ms / 45s')
    expect(html).toContain('1 / 1 节点启用')
    expect(html).not.toContain('8.8.8.8:')
  })

  it('renders HTTP GET monitor targets without requiring a port', () => {
    const html = renderAdmin('targets')

    expect(html).toContain('Zeno Health HTTP')
    expect(html).not.toContain('HTTP GET')
    expect(html).toContain('https://example.com/health')
    expect(html).not.toContain('2 次 / 1500ms / 60s')
    expect(html).toContain('1 / 1 节点启用')
    expect(html).not.toContain('https://example.com/health:')
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
