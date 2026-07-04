import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { AdminDashboard, HomeTopPanel, reconcileAlertRuleStates, reconcileAlertRuleStatesForNode, shellStyleForSettings } from './App'
import type { AdminAlertRule, AdminAlertRuleState, AdminMaintenance, AdminNode, AdminNotificationChannel, AdminNotificationDelivery, AdminNotificationType, AdminProbeTarget, AdminSettings } from './types'

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

const notificationTypes: AdminNotificationType[] = [
  { eventType: 'node_online', label: '上线', enabled: true, updatedAt: '2026-07-03T00:00:00Z' },
  { eventType: 'node_offline', label: '离线', enabled: false },
  { eventType: 'probe_unhealthy', label: '异常', enabled: false },
]

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
    createdAt: '2026-07-03T00:00:00Z',
    updatedAt: '2026-07-03T00:00:00Z',
  },
]

const notificationDeliveries: AdminNotificationDelivery[] = [
  {
    id: 7,
    eventType: 'node_online',
    label: '上线',
    nodeId: 'hytron',
    nodeName: 'Hytron',
    previousStatus: 'no_data',
    status: 'online',
    channelId: 'zeno-telegram',
    channelName: 'Zeno Telegram',
    success: false,
    error: 'telegram returned status 500',
    createdAt: '2026-07-03T00:05:00Z',
  },
]

const alertRuleStates: AdminAlertRuleState[] = [
  {
    nodeId: 'hytron',
    nodeName: 'Hytron',
    nodeStatus: 'warning',
    ruleId: 'cpu_high',
    ruleName: 'CPU 使用率',
    category: 'resource',
    metric: 'cpu_percent',
    comparator: '>=',
    threshold: 90,
    thresholdUnit: '%',
    durationSec: 300,
    enabled: true,
    lastValue: 95.25,
    active: true,
    notificationEventType: 'probe_unhealthy',
    notificationLabel: '异常',
    firstSeenAt: '2026-07-04T11:00:00Z',
    lastSeenAt: '2026-07-04T11:00:00Z',
    updatedAt: '2026-07-04T11:00:01Z',
  },
]

const settings: AdminSettings = {
  siteTitle: '水饺监控',
  siteSubtitle: 'VPS 状态总览',
  logoUrl: '/assets/logo/custom.png',
  theme: 'dark',
  backgroundUrl: 'https://example.com/desktop-bg.webp',
  desktopBackgroundUrl: 'https://example.com/desktop-bg.webp',
  mobileBackgroundUrl: 'https://example.com/mobile-bg.webp',
  updatedAt: '2026-07-04T12:00:00Z',
}

const maintenance: AdminMaintenance = {
  settings: {
    enabled: true,
    stateRetentionDays: 30,
    probeRetentionDays: 45,
    notificationRetentionDays: 90,
    updatedAt: '2026-07-04T13:00:00Z',
  },
  candidates: {
    stateSamples: 12,
    probeRounds: 3,
    probeSamples: 9,
    notificationDeliveries: 2,
  },
}

function renderAdmin(section: 'overview' | 'nodes' | 'targets' | 'notifications' | 'rules' | 'maintenance' | 'settings' = 'overview') {
  return renderToStaticMarkup(
    <AdminDashboard
      onHome={() => {}}
      settings={settings}
      hasAdminToken
      initialSection={section}
      adminState={{
        kind: 'ready',
        nodes: [hytronNode, backupNode],
        targets: [hytronTarget, pingTarget, httpTarget],
        notificationChannels: [telegramChannel],
        notificationTypes,
        notificationDeliveries,
        alertRules,
        alertRuleStates,
        maintenance,
      }}
      onAdminTokenSubmit={() => {}}
      onAdminTokenClear={() => {}}
      onAdminRefresh={() => {}}
      onAdminNodeCreate={() => {}}
      onAdminNodeUpdate={() => {}}
      onAdminInstallCommand={async () => 'install command'}
      onAdminProbeTargetCreate={() => {}}
      onAdminProbeTargetUpdate={() => {}}
      onAdminNotificationChannelCreate={() => {}}
      onAdminNotificationChannelUpdate={() => {}}
      onAdminNotificationTypeUpdate={() => {}}
      onAdminAlertRuleUpdate={() => {}}
      onAdminSettingsUpdate={() => {}}
      onAdminMaintenanceUpdate={() => {}}
      onAdminMaintenanceCleanup={async () => ({ ...maintenance, deleted: maintenance.candidates, dryRun: true })}
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
  it('uses the same card shell and introduces separated backend navigation', () => {
    const html = renderAdmin()

    expect(html).toContain('home-top-card')
    expect(html).toContain('admin-panel')
    expect(html).toContain('Zeno 后台')
    expect(html).toContain('列表只保留关键字段')
    expect(html).toContain('admin-section-nav')
    expect(html).toContain('后台导航')
    expect(html).toContain('概览')
    expect(html).toContain('服务器')
    expect(html).toContain('延迟监控')
    expect(html).toContain('状态规则')
    expect(html).toContain('数据维护')
    expect(html).toContain('设置')
    expect(html).toContain('通知')
  })

  it('renders data maintenance as its own section with retention settings and cleanup actions', () => {
    const html = renderAdmin('maintenance')

    expect(html).toContain('数据维护')
    expect(html).toContain('候选数据')
    expect(html).toContain('状态采样')
    expect(html).toContain('12 条')
    expect(html).toContain('探测轮次')
    expect(html).toContain('3 条')
    expect(html).toContain('探测明细')
    expect(html).toContain('9 条')
    expect(html).toContain('通知发送')
    expect(html).toContain('2 条')
    expect(html).toContain('name="maintenance-enabled"')
    expect(html).toContain('name="maintenance-state-retention-days"')
    expect(html).toContain('name="maintenance-probe-retention-days"')
    expect(html).toContain('name="maintenance-notification-retention-days"')
    expect(html).toContain('保存数据维护设置')
    expect(html).toContain('预览清理')
    expect(html).toContain('确认清理')
  })

  it('renders settings as a lightweight appearance configuration page', () => {
    const html = renderAdmin('settings')

    expect(html).toContain('站点设置')
    expect(html).toContain('外观配置')
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
    expect(html).toContain('name="desktop-background-url"')
    expect(html).toContain('https://example.com/desktop-bg.webp')
    expect(html).toContain('name="mobile-background-url"')
    expect(html).toContain('https://example.com/mobile-bg.webp')
    expect(html).not.toContain('token')
    expect(html).not.toContain('secret')
    expect(html).not.toContain('credential')
    expect(html).not.toContain('hash')
  })

  it('renders real notification channels and types instead of a placeholder', () => {
    const html = renderAdmin('notifications')

    expect(html).toContain('通知渠道')
    expect(html).toContain('通知类型')
    expect(html).toContain('Zeno Telegram')
    expect(html).toContain('7579942307')
    expect(html).toContain('凭据已设置')
    expect(html).toContain('node_online')
    expect(html).toContain('上线')
    expect(html).toContain('启用中')
    expect(html).toContain('添加通知渠道')
    expect(html).toContain('编辑渠道')
    expect(html).toContain('删除渠道')
    expect(html).toContain('测试发送')
    expect(html).toContain('最近发送')
    expect(html).toContain('Hytron')
    expect(html).toContain('发送失败')
    expect(html).toContain('telegram returned status 500')
    expect(html).not.toContain('后续再接入')
    expect(html).not.toContain('telegram-bot-secret')
    expect(html).not.toContain('告警')
  })

  it('renders status rules as their own notification-rule management page with current active hits', () => {
    const html = renderAdmin('rules')

    expect(html).toContain('状态规则')
    expect(html).toContain('通知规则')
    expect(html).toContain('当前异常')
    expect(html).toContain('Hytron')
    expect(html).toContain('warning')
    expect(html).toContain('当前值 95.25%')
    expect(html).toContain('CPU 使用率')
    expect(html).toContain('cpu_percent')
    expect(html).toContain('&gt;= 90%')
    expect(html).toContain('持续 300s')
    expect(html).toContain('通知：异常')
    expect(html).toContain('离线判定')
    expect(html).toContain('node_offline')
    expect(html).toContain('编辑规则')
    expect(html).toContain('停用规则')
    expect(html).not.toContain('告警')
    expect(html).not.toContain('telegram-bot-secret')
  })

  it('reconciles current anomalies immediately when a status rule is disabled or its threshold changes', () => {
    const [disabledState] = reconcileAlertRuleStates({ ...alertRules[0], enabled: false, threshold: 85, durationSec: 120 }, alertRuleStates)

    expect(disabledState.enabled).toBe(false)
    expect(disabledState.active).toBe(false)
    expect(disabledState.threshold).toBe(85)
    expect(disabledState.durationSec).toBe(120)

    const [belowRaisedThreshold] = reconcileAlertRuleStates({ ...alertRules[0], threshold: 99 }, alertRuleStates)
    expect(belowRaisedThreshold.enabled).toBe(true)
    expect(belowRaisedThreshold.active).toBe(false)

    const legacyState: AdminAlertRuleState = { ...alertRuleStates[0], lastValue: null, active: true }
    const [legacyWithoutValue] = reconcileAlertRuleStates({ ...alertRules[0], threshold: 99 }, [legacyState])
    expect(legacyWithoutValue.active).toBe(true)
  })

  it('reconciles current anomalies immediately when a node is disabled', () => {
    const [state] = reconcileAlertRuleStatesForNode({ ...hytronNode, disabled: true, status: 'disabled', displayName: 'Hytron Renamed' }, alertRuleStates)

    expect(state.nodeName).toBe('Hytron Renamed')
    expect(state.nodeStatus).toBe('disabled')
    expect(state.active).toBe(false)
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
    expect(html).toContain('198.51.100.8')
    expect(html).toContain('2001:db8::8')
    expect(html).toContain('2026-08-01')
    expect(html).toContain('月付')
    expect(html).toContain('顺序 10')
    expect(html).toContain('🇭🇰')
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
    expect(html).toContain('按名称排序')
    expect(html).toContain('hytron-local')
    expect(html).toContain('127.0.0.1:18980')
    expect(html).toContain('3 次 / 1200ms / 60s')
    expect(html).toContain('1 / 2 节点启用')
    expect(html).toContain('编辑目标')
    expect(html).toContain('停用目标')
    expect(html).toContain('删除目标')
    expect(html).toContain('全节点启用')
    expect(html).toContain('全节点停用')
    expect(html.indexOf('Example ICMP')).toBeLessThan(html.indexOf('Hytron'))
    expect(html).not.toContain('admin-target-card')
    expect(html).not.toContain('name="target-name"')
    expect(html).not.toContain('保存目标')
    expect(html).not.toContain('admin-pass')
  })

  it('renders ping monitor targets without requiring a port', () => {
    const html = renderAdmin('targets')

    expect(html).toContain('Example ICMP')
    expect(html).toContain('ICMP Ping')
    expect(html).toContain('8.8.8.8')
    expect(html).toContain('4 次 / 900ms / 45s')
    expect(html).toContain('1 / 1 节点启用')
    expect(html).not.toContain('8.8.8.8:')
  })

  it('renders HTTP GET monitor targets without requiring a port', () => {
    const html = renderAdmin('targets')

    expect(html).toContain('Zeno Health HTTP')
    expect(html).toContain('HTTP GET')
    expect(html).toContain('https://example.com/health')
    expect(html).toContain('2 次 / 1500ms / 60s')
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
