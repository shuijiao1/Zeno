import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { AdminCredentialField, AdminDashboard, AdminDeleteConfirmModal, HomeTopPanel, adminTokenMaxAgeMs, applyCustomCode, documentBrandingForSettings, homeTrafficTotalsForNodes, isAdminUnauthorizedError, orderHomeNodes, remoteInsecureAgentControllerURL, shellStyleForSettings, shouldRefreshHomeRealtimeSnapshot, validateAdminSettingsInput } from './App'
import type { AdminAlertRule, AdminNode, AdminNotificationChannel, AdminProbeTarget, AdminSettings, HomeCardNode } from './types'

const overviewProps = {
  totalCount: 11,
  onlineCount: 9,
  offlineCount: 2,
  totalUp: 1024,
  totalDown: 2048,
  upSpeed: 128,
  downSpeed: 256,
}

describe('remoteInsecureAgentControllerURL', () => {
  it('only marks non-loopback HTTP origins as plaintext remote transport', () => {
    expect(remoteInsecureAgentControllerURL('http://203.0.113.10:18980')).toBe(true)
    expect(remoteInsecureAgentControllerURL('http://[2001:db8::10]:18980')).toBe(true)
    expect(remoteInsecureAgentControllerURL('http://localhost:18980')).toBe(false)
    expect(remoteInsecureAgentControllerURL('http://127.0.0.2:18980')).toBe(false)
    expect(remoteInsecureAgentControllerURL('http://[::1]:18980')).toBe(false)
    expect(remoteInsecureAgentControllerURL('https://zeno.example.com')).toBe(false)
  })
})

describe('AdminDeleteConfirmModal', () => {
  it('keeps the delete confirmation short and names the subject', () => {
    const html = renderToStaticMarkup(
      <AdminDeleteConfirmModal
        title="删除延迟监控"
        subjectName="Zeno Health"
        confirmLabel="删除延迟监控"
        onConfirm={() => {}}
        onClose={() => {}}
      />,
    )

    expect(html).toContain('class="admin-modal admin-delete-modal"')
    expect(html).toContain('role="dialog"')
    expect(html).toContain('aria-describedby=')
    expect(html).toContain('aria-busy="false"')
    expect(html).toContain('确认删除')
    expect(html).toContain('Zeno Health')
    expect(html).toContain('删除后无法恢复。')
    expect(html).not.toContain('影响范围')
    expect(html).not.toContain('确认后将立即执行删除。')
    expect(html).toContain('class="is-danger" type="submit"')
  })
})

const trafficNode: HomeCardNode = {
  id: 'traffic-node',
  displayName: 'Traffic Node',
  status: 'online',
  os: 'debian',
  cpuPercent: 1,
  memoryUsedBytes: 1,
  memoryTotalBytes: 2,
  diskUsedBytes: 1,
  diskTotalBytes: 2,
  netInSpeedBps: 1,
  netOutSpeedBps: 1,
  netInTotalBytes: 100,
  netOutTotalBytes: 200,
  netInLifetimeBytes: 1_000,
  netOutLifetimeBytes: 2_000,
  monthlyBillableBytes: 1,
  monthlyQuotaBytes: 2,
}

describe('homeTrafficTotalsForNodes', () => {
  it('uses controller-persisted lifetime traffic instead of reboot-scoped interface counters', () => {
    expect(homeTrafficTotalsForNodes([trafficNode, { ...trafficNode, id: 'second', netInLifetimeBytes: 3_000, netOutLifetimeBytes: 4_000 }])).toEqual({
      totalUp: 6_000,
      totalDown: 4_000,
    })
  })

  it('falls back to raw counters for a cached summary created before lifetime totals existed', () => {
    const legacyNode = { ...trafficNode, netInLifetimeBytes: undefined, netOutLifetimeBytes: undefined }
    expect(homeTrafficTotalsForNodes([legacyNode])).toEqual({ totalUp: 200, totalDown: 100 })
  })
})

const exampleNodeANode: AdminNode = {
  id: 'example-node-a',
  displayName: 'Example Node A',
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
  hostname: 'example-node-a-real',
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
  ...exampleNodeANode,
  id: 'backup',
  displayName: 'Backup',
  status: 'no_data',
  countryCode: undefined,
  region: undefined,
  agentVersion: undefined,
}

const exampleNodeATarget: AdminProbeTarget = {
  id: 'example-node-a-local',
  name: 'Example Node A',
  type: 'tcping',
  address: '127.0.0.1',
  port: 18980,
  count: 3,
  timeoutMs: 1200,
  intervalSec: 60,
  displayOrder: 20,
  enabled: true,
  assignments: [
    { nodeId: 'example-node-a', nodeDisplayName: 'Example Node A', enabled: true },
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
    { nodeId: 'example-node-a', nodeDisplayName: 'Example Node A', enabled: true },
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
    { nodeId: 'example-node-a', nodeDisplayName: 'Example Node A', enabled: true },
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
    description: '',
    scopeNodeIds: [],
    createdAt: '2026-07-03T00:00:00Z',
    updatedAt: '2026-07-03T00:00:00Z',
  },
  {
    id: 'node_offline',
    name: '离线通知',
    category: 'liveness',
    metric: 'heartbeat_age_sec',
    comparator: '>=',
    threshold: 180,
    thresholdUnit: 's',
    durationSec: 30,
    enabled: true,
    notificationEventType: 'node_offline',
    notificationLabel: '离线',
    description: '',
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
  appearancePreset: 'gaussian_blur',
  cardOpacity: 0.58,
  cardBlur: 18,
  cardRadius: 24,
  borderStrength: 0.34,
  shadowStrength: 0.34,
  backgroundOverlay: 0.08,
  themeColor: '#6366f1',
  customCode: '<style>.home-top-card { border-color: #2563eb; }</style><script>window.ZenoCustomLoaded = true;</script>',
  updatedAt: '2026-07-04T12:00:00Z',
}

function renderAdmin(section: 'nodes' | 'targets' | 'notifications' | 'account' | 'settings' = 'nodes', authState: { kind: 'idle' } | { kind: 'loading' } | { kind: 'error'; message: string } = { kind: 'idle' }) {
  return renderToStaticMarkup(
    <AdminDashboard
      onHome={() => {}}
      settings={settings}
      hasAdminToken
      authState={authState}
      initialSection={section}
      adminState={{
        kind: 'ready',
        account: { username: 'admin' },
        nodes: [exampleNodeANode, backupNode],
        targets: [exampleNodeATarget, pingTarget, httpTarget],
        notificationChannels: [telegramChannel],
        alertRules,
      }}
      onAdminLogin={() => {}}
      onAdminTokenClear={() => {}}
      onAdminAccountUpdate={async () => {}}
      onAdminNodeCreate={async () => undefined}
      onAdminNodeUpdate={() => {}}
      onAdminInstallCommand={async () => ({ nodeId: 'example-node-a', command: 'install command', commands: { linux: 'install command' } })}
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
  it('keeps local admin tokens for at most one day', () => {
    expect(adminTokenMaxAgeMs).toBe(24 * 60 * 60 * 1000)
  })

  it('recognizes expired admin session API responses', () => {
    expect(isAdminUnauthorizedError(new Error('admin nodes request failed: 401'))).toBe(true)
    expect(isAdminUnauthorizedError(new Error('admin settings update failed: 401'))).toBe(true)
    expect(isAdminUnauthorizedError(new Error('admin logout failed: 401'))).toBe(true)
    expect(isAdminUnauthorizedError(new Error('admin nodes request failed: 500'))).toBe(false)
    expect(isAdminUnauthorizedError(new Error('missing admin token'))).toBe(false)
  })

  it('paces aggregate homepage realtime refreshes on live summary frames', () => {
    expect(shouldRefreshHomeRealtimeSnapshot(null, 100, 100)).toBe(true)
    expect(shouldRefreshHomeRealtimeSnapshot(100, 800, 100)).toBe(true)
    expect(shouldRefreshHomeRealtimeSnapshot(100, 1800, 100)).toBe(false)
    expect(shouldRefreshHomeRealtimeSnapshot(100, 1950, 100)).toBe(true)
    expect(shouldRefreshHomeRealtimeSnapshot(100, 2100, 100)).toBe(true)
  })

  it('keeps configured order inside online/offline groups but moves offline homepage cards last', () => {
    const nodes = [
      { id: 'offline-first', status: 'offline' },
      { id: 'online-middle', status: 'online' },
      { id: 'warning-last', status: 'warning' },
    ] as HomeCardNode[]

    expect(orderHomeNodes(nodes).map((node) => node.id)).toEqual(['online-middle', 'offline-first', 'warning-last'])
    expect(nodes.map((node) => node.id)).toEqual(['offline-first', 'online-middle', 'warning-last'])
  })

  it('uses the configured logo as the browser favicon source', () => {
    expect(documentBrandingForSettings(settings)).toEqual({
      title: '水饺监控',
      iconHref: '/assets/logo/custom.png',
    })
  })

  it('turns configured desktop and mobile background images into safe shell variables', () => {
    expect(shellStyleForSettings(settings)).toEqual({
      '--zeno-desktop-background-image': 'url("https://example.com/desktop-bg.webp")',
      '--zeno-mobile-background-image': 'url("https://example.com/mobile-bg.webp")',
      '--zeno-mobile-background-size': 'contain',
      '--blue': '#6366f1',
      '--border': 'rgba(99, 102, 241, 0.340)',
      '--metric-shadow': 'rgba(99, 102, 241, 0.075)',
      '--surface-strong': 'rgba(15, 23, 42, 0.580)',
      '--surface': 'rgba(15, 23, 42, 0.480)',
      '--surface-soft': 'rgba(15, 23, 42, 0.240)',
      '--secondary': 'rgba(15, 23, 42, 0.320)',
      '--metric-bg': 'rgba(15, 23, 42, 0.380)',
      '--field-bg': 'rgba(15, 23, 42, 0.440)',
      '--control-bg': 'rgba(15, 23, 42, 0.480)',
      '--radius-panel': '24px',
      '--radius-card': '20px',
      '--radius-field': '16px',
      '--zeno-card-blur': '18px',
      '--zeno-card-highlight': 'rgba(255, 255, 255, 0.081)',
      '--zeno-card-shadow': '0 10px 26px -24px rgba(0, 0, 0, 0.190), 0 1px 2px rgba(0, 0, 0, 0.037)',
      '--zeno-background-overlay-color': 'rgba(0, 0, 0, 0.080)',
      '--zeno-theme-rgb': '99, 102, 241',
      backgroundSize: 'cover',
      backgroundAttachment: 'fixed',
    })
    expect(shellStyleForSettings({ ...settings, backgroundUrl: '', desktopBackgroundUrl: '', mobileBackgroundUrl: '' })).toMatchObject({
      '--zeno-desktop-background-image': 'none',
      '--zeno-card-blur': '18px',
    })
    expect(shellStyleForSettings({ ...settings, mobileBackgroundUrl: '' })).toMatchObject({
      '--zeno-mobile-background-image': 'url("https://example.com/desktop-bg.webp")',
      '--zeno-mobile-background-size': 'cover',
    })
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
    expect(html).toContain('后台')
    expect(html).not.toContain('aria-label="language"')
    expect(html).not.toContain('Zeno Overview')
    expect(html).toContain('水饺监控')
    expect(html).toContain('/assets/logo/custom.png')
    expect(html).not.toContain('/assets/avatar/custom.webp')
    expect(html).not.toContain('VPS 状态总览')
    expect(html).toContain('home-summary')
    expect(html).toContain('home-summary__status-line')
    expect(html).toContain('9 / 11 在线')
    expect(html).toContain('home-summary__metric--send')
    expect(html).toContain('home-summary__metric--receive')
    expect(html).toContain('home-summary__metric--upload-rate')
    expect(html).toContain('home-summary__metric--download-rate')
    expect(html).not.toContain('Zeno Overview')
    expect(html).not.toContain('服务器运行概览')
    expect(html).not.toContain('在线率')
    expect(html).not.toContain('2 台未在线')
    expect(html).not.toContain('11 台服务器')
    expect(html).toContain('发送')
    expect(html).toContain('接收')
    expect(html).toContain('上传')
    expect(html).toContain('下载')
    expect(html).toContain('home-summary__rate-gap')
    expect(html).not.toContain('CircleArrowIcon')
    expect(html).not.toContain('实时')
    expect(html).not.toContain('累计上传')
    expect(html).not.toContain('累计下载')
    expect(html).not.toContain('服务器总数')
    expect(html).not.toContain('在线服务器')
    expect(html).not.toContain('离线服务器')
    expect(html).not.toContain('overview-card--combined')
    expect(html).not.toContain('overview-metric')
    expect(html).not.toContain(['service', 'status', 'panel'].join('-'))
  })

  it('renders a circular Z brand avatar when the logo URL is blank', () => {
    const html = renderToStaticMarkup(
      <HomeTopPanel
        {...overviewProps}
        settings={{ ...settings, logoUrl: '' }}
        onHome={() => {}}
        onAdmin={() => {}}
      />,
    )

    expect(html).toContain('class="brand-logo-fallback"')
    expect(html).toContain('role="img"')
    expect(html).toContain('aria-label="水饺监控 logo"')
    expect(html).toContain('>Z</span>')
  })

  it('always renders the background control and enables it only when it is ready', () => {
    const waiting = renderToStaticMarkup(<HomeTopPanel {...overviewProps} settings={settings} onHome={() => {}} onAdmin={() => {}} />)
    expect(waiting).toContain('aria-label="背景图未配置"')
    expect(waiting).toContain('aria-pressed="false"')
    expect(waiting).toContain('disabled=""')
    expect(waiting).not.toContain('nav-icon-button-placeholder')

    const loading = renderToStaticMarkup(
      <HomeTopPanel {...overviewProps} settings={settings} onHome={() => {}} onAdmin={() => {}} backgroundEnabled />,
    )
    expect(loading).toContain('aria-label="背景图加载中"')
    expect(loading).toContain('aria-pressed="true"')
    expect(loading).toContain('disabled=""')
    expect(loading).toContain('nav-icon-button is-solid')

    const ready = renderToStaticMarkup(
      <HomeTopPanel {...overviewProps} settings={settings} onHome={() => {}} onAdmin={() => {}} onBackgroundToggle={() => {}} backgroundEnabled={false} />,
    )
    expect(ready).toContain('aria-label="开启背景图"')
    expect(ready).toContain('aria-pressed="false"')
    expect(ready).not.toContain('disabled=""')
    expect(ready).not.toContain('nav-icon-button-placeholder')
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

    expect(html).toContain('0 / 11 在线')
    expect(html).not.toContain('11 台未在线')
    expect(html).not.toContain('11 台服务器')
    expect(html).not.toContain('全部在线')
  })
})

describe('AdminDashboard', () => {
  it('keeps Telegram credentials masked with an accessible visibility toggle and no reflected value', () => {
    const html = renderToStaticMarkup(
      <AdminCredentialField name="channel-credential" placeholder="留空则保留已保存 Token" />,
    )

    expect(html).toContain('name="channel-credential"')
    expect(html).toContain('type="password"')
    expect(html).toContain('autoComplete="new-password"')
    expect(html).toContain('class="admin-secret-toggle"')
    expect(html).toContain('aria-label="显示 Telegram Bot Token"')
    expect(html).toContain('aria-pressed="false"')
    expect(html).not.toContain('value=')
    expect(html).not.toContain('telegram-bot-secret')
  })

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
    expect(html).not.toContain('10 台')
    expect(html).not.toContain('13 个目标')
    expect(html).not.toContain('2 类型')
    expect(html).not.toContain('1 异常 / 2 类型')
    expect(html).toContain('账户')
    expect(html).toContain('设置')
    expect(html).toContain('通知')
    expect(html).toContain('退出')
    expect(html).not.toContain(['刷', '新'].join(''))
    expect(html).not.toContain('修改密码</button>')
    expect(html).toContain('服务器列表')
    expect(html).toContain('Example Node A')
    expect(html).not.toContain('admin-overview-panel')
  })

  it('keeps the logged-in dashboard visible and shows logout failures', () => {
    const html = renderAdmin('nodes', { kind: 'error', message: '退出失败：admin logout failed: 500' })

    expect(html).toContain('nav-logout-button')
    expect(html).toContain('退出失败：admin logout failed: 500')
    expect(html).toContain('admin-section-nav')
    expect(html).toContain('服务器列表')
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
    expect(html).toContain('placeholder="可留空"')
    expect(html).not.toContain('留空显示字母 Z')
    expect(html).toContain('/assets/logo/custom.png')
    expect(html).toContain('头像 / Logo URL')
    expect(html).not.toContain('/assets/avatar/custom.webp')
    expect(html).not.toContain('name="avatar-url"')
    expect(html).toContain('name="theme"')
    expect(html).toContain('深色')
    expect(html).toContain('Agent 接入 URL')
    expect(html).toContain('name="agent-controller-url"')
    expect(html).toContain('https://zeno.example.com')
    expect(html).not.toContain(['图片字段', '只填 https:// 链接或 /assets/... 站内路径'].join(''))
    expect(html).not.toContain(['最近', '更新：'].join(''))
    expect(html).toContain('name="desktop-background-url"')
    expect(html).toContain('https://example.com/desktop-bg.webp')
    expect(html).toContain('name="mobile-background-url"')
    expect(html).toContain('https://example.com/mobile-bg.webp')
    expect(html).toContain('外观样式')
    expect(html).toContain('admin-appearance-top')
    expect(html).toContain('name="appearance-preset"')
    expect(html).toContain('高斯模糊主题')
    expect(html).toContain('name="card-opacity"')
    expect(html).toContain('卡片透明度')
    expect(html).toContain('name="card-blur"')
    expect(html).toContain('卡片模糊度')
    expect(html).toContain('name="card-radius"')
    expect(html).toContain('卡片圆角')
    expect(html).toContain('name="border-strength"')
    expect(html).toContain('边框强度')
    expect(html).toContain('name="shadow-strength"')
    expect(html).toContain('阴影强度')
    expect(html).toContain('name="background-overlay"')
    expect(html).toContain('背景遮罩')
    expect(html).toContain('name="theme-color"')
    expect(html).toContain('自定义 CSS')
    expect(html).toContain('name="custom-code"')
    expect(html).toContain('&lt;style&gt;.home-top-card { border-color: #2563eb; }&lt;/style&gt;')
    expect(html).toContain('&lt;script&gt;window.ZenoCustomLoaded = true;&lt;/script&gt;')
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
      appearancePreset: 'default' as const,
      cardOpacity: 0.72,
      cardBlur: 0,
      cardRadius: 20,
      borderStrength: 0.26,
      shadowStrength: 0.22,
      backgroundOverlay: 0,
      themeColor: '#2563eb',
      customCode: '',
    }

    expect(validateAdminSettingsInput(baseInput)).toBeNull()
    expect(validateAdminSettingsInput({ ...baseInput, logoUrl: '' })).toBeNull()
    expect(validateAdminSettingsInput({ ...baseInput, logoUrl: 'http://example.com/logo.png' })).toContain('头像 / Logo URL')
    expect(validateAdminSettingsInput({ ...baseInput, desktopBackgroundUrl: 'javascript:alert(1)' })).toContain('电脑端背景图 URL')
    expect(validateAdminSettingsInput({ ...baseInput, mobileBackgroundUrl: '//example.com/bg.png' })).toContain('手机端背景图 URL')
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'https://user:pass@example.com' })).toContain('Agent 接入 URL')
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'https://zeno.example.com/?token=1' })).toContain('Agent 接入 URL')
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'http://203.0.113.10:18980' })).toBeNull()
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'http://[2001:db8::10]:18980' })).toBeNull()
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'http://203.0.113.10' })).toContain('Agent 接入 URL')
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'http://zeno.example.com:18980' })).toContain('Agent 接入 URL')
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'http://127.0.0.1:18980' })).toBeNull()
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'https://zeno.example.com/' })).toBeNull()
    expect(validateAdminSettingsInput({ ...baseInput, appearancePreset: 'other' as never })).toContain('外观模板')
    expect(validateAdminSettingsInput({ ...baseInput, cardOpacity: 0.1 })).toContain('卡片透明度')
    expect(validateAdminSettingsInput({ ...baseInput, cardBlur: 41 })).toContain('卡片模糊度')
    expect(validateAdminSettingsInput({ ...baseInput, cardRadius: 7 })).toContain('卡片圆角')
    expect(validateAdminSettingsInput({ ...baseInput, borderStrength: 1.1 })).toContain('边框强度')
    expect(validateAdminSettingsInput({ ...baseInput, shadowStrength: 1.1 })).toContain('阴影强度')
    expect(validateAdminSettingsInput({ ...baseInput, backgroundOverlay: 0.9 })).toContain('背景遮罩')
    expect(validateAdminSettingsInput({ ...baseInput, themeColor: 'blue' })).toContain('主题色')
    expect(validateAdminSettingsInput({ ...baseInput, customCode: 'a'.repeat(60001) })).toContain('自定义代码')
  })

  it('applies custom code through managed document nodes', () => {
    type TestElement = {
      id: string
      type: string
      hidden: boolean
      nodeName: string
      textContent: string | null
      attributes: Array<{ name: string; value: string }>
      childNodes: TestElement[]
      appendChild: (child: TestElement) => TestElement
      setAttribute: (name: string, value: string) => void
      remove: () => void
    }
    const documentStub = {
      customNodes: [] as TestElement[],
      head: {
        children: [] as TestElement[],
        appendChild(element: TestElement) {
          this.children.push(element)
          return element
        },
      },
      body: {
        children: [] as TestElement[],
        appendChild(element: TestElement) {
          this.children.push(element)
          return element
        },
      },
      createElement(tag: string): TestElement | { nodeName: string; content: { childNodes: TestElement[] }; innerHTML: string } {
        const makeElement = (nodeName: string): TestElement => {
          const element: TestElement = {
            id: '',
            type: '',
            hidden: false,
            nodeName,
            textContent: null,
            attributes: [],
            childNodes: [],
            appendChild(child: TestElement) {
              this.childNodes.push(child)
              return child
            },
            setAttribute(name: string, value: string) {
              this.attributes.push({ name, value })
              if (name === 'data-zeno-custom-code') documentStub.customNodes.push(element)
            },
            remove() {
              documentStub.head.children = documentStub.head.children.filter((child) => child !== element)
              documentStub.body.children = documentStub.body.children.filter((child) => child !== element)
              documentStub.customNodes = documentStub.customNodes.filter((child) => child !== element)
            },
          }
          return element
        }
        if (tag === 'template') {
          const style = makeElement('STYLE')
          style.textContent = '.home-top-card { border-color: #2563eb; }'
          const script = makeElement('SCRIPT')
          script.textContent = 'window.ZenoCustomLoaded = true;'
          return { nodeName: 'TEMPLATE', content: { childNodes: [style, script] }, innerHTML: '' }
        }
        return makeElement(tag.toUpperCase())
      },
      querySelectorAll(selector: string) {
        return selector === '[data-zeno-custom-code]' ? this.customNodes : []
      },
    }
    const previousDocument = globalThis.document
    try {
      Object.defineProperty(globalThis, 'document', { value: documentStub, configurable: true })
      applyCustomCode(settings)
      expect(documentStub.body.children.some((child) => child.textContent === 'window.ZenoCustomLoaded = true;')).toBe(false)
      expect(documentStub.head.children.some((child) => child.nodeName === 'STYLE' && child.textContent === '.home-top-card { border-color: #2563eb; }')).toBe(true)
      applyCustomCode({ ...settings, customCode: '' })
      expect(documentStub.querySelectorAll('[data-zeno-custom-code]')).toHaveLength(0)
    } finally {
      Object.defineProperty(globalThis, 'document', { value: previousDocument, configurable: true })
    }
  })

  it('renders real notification channels and types instead of a placeholder', () => {
    const html = renderAdmin('notifications')

    expect(html).toContain('通知渠道')
    expect(html).toContain('通知类型')
    expect(html).toContain('Zeno Telegram')
    expect(html).not.toContain('接收人')
    expect(html).not.toContain('Bot Token</span>')
    expect(html).not.toContain('凭据已设置')
    expect(html).toContain('添加通知类型')
    expect(html).toContain('<button class="admin-primary-action" type="button">添加通知类型</button>')
    expect(html).toContain('CPU 使用率')
    expect(html).toContain('启用中')
    expect(html).toContain('添加通知渠道')
    expect(html).toContain('aria-label="编辑通知渠道 Zeno Telegram"')
    expect(html).toContain('aria-label="删除通知渠道 Zeno Telegram"')
    expect(html).not.toContain('停用渠道')
    expect(html).not.toContain('启用渠道')
    expect(html).not.toContain('<button class="admin-row-action" type="button">测试发送</button>')
    expect(html).not.toContain('zeno-telegram')
    expect(html).not.toContain('7579942307')
    expect(html).not.toContain('telegram-bot-secret')
    expect(html).not.toContain('告警')
  })

  it('renders notification type triggers in the notifications section', () => {
    const html = renderAdmin('notifications')

    expect(html).toContain('通知类型')
    expect(html).toContain('CPU 使用率')
    expect(html).not.toContain('data-label="范围"')
    expect(html).not.toContain('全部服务器')
    expect(html).toContain('离线通知')
    expect(html).not.toContain('node_offline')
    expect(html).toContain('添加通知类型')
    expect(html).toContain('aria-label="编辑通知类型 CPU 使用率"')
    expect(html).toContain('aria-label="删除通知类型 CPU 使用率"')
    expect(html).not.toContain('移除')
    expect(html).not.toContain('cpu_high · 资源')
    expect(html).not.toContain('触发条件</h4>')
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
    expect(html).toContain('Example Node A')
    expect(html).not.toContain('<span>状态</span>')
    expect(html).not.toContain('data-label="状态"')
    expect(html).toContain('Agent 版本')
    expect(html).toContain('agent-test')
    expect(html).toContain('198.51.100.8')
    expect(html).toContain('2001:db8::8')
    expect(html).not.toContain('v4 198.51.100.8')
    expect(html).not.toContain('v6 2001:db8::8')
    expect(html).toContain('admin-ip-stack')
    expect(html).not.toContain('debian 13')
    expect(html).not.toContain('2026-08-01')
    expect(html).not.toContain('月付')
    expect(html).not.toContain('example-harbor · 🇭🇰 HK · 顺序 10')
    expect(html).not.toContain('顺序 10')
    expect(html).toContain('服务器排序')
    expect(html).not.toContain('name="node-sort"')
    expect(html).not.toContain('按状态排序')
    expect(html).not.toContain('按 Agent 排序')
    expect(html).not.toContain('按公网 IP 排序')
    expect(html).not.toContain('整理顺序')
    expect(html).not.toContain('上移')
    expect(html).not.toContain('下移')
    expect(html).toContain('aria-label="编辑服务器 Example Node A"')
    expect(html).toContain('aria-label="删除服务器 Example Node A"')
    expect(html).toContain('admin-row-action is-icon')
    expect(html).toContain('admin-row-action is-icon is-danger')
    expect(html).not.toContain('admin-node-card')
    expect(html).not.toContain('name="display-name"')
    expect(html).not.toContain('保存服务器')
    expect(html).not.toContain('admin-pass')
  })

  it('keeps latency monitor management on its own list page', () => {
    const html = renderAdmin('targets')

    expect(html).toContain('延迟监控')
    expect(html).toContain('admin-target-list')
    expect(html).not.toContain('name="target-sort"')
    expect(html).not.toContain('按手动顺序')
    expect(html).not.toContain('按名称排序')
    expect(html).not.toContain('按启用状态排序')
    expect(html).not.toContain('整理顺序')
    expect(html).not.toContain('<span>状态</span>')
    expect(html).not.toContain('data-label="状态"')
    expect(html).not.toContain('>启用中<')
    expect(html).not.toContain('example-node-a-local')
    expect(html).not.toContain('顺序 20')
    expect(html).toContain('127.0.0.1:18980')
    expect(html).not.toContain('3 次 / 1200ms / 60s')
    expect(html).toContain('1 / 2 服务器启用')
    expect(html).toContain('aria-label="编辑目标 Example Node A"')
    expect(html).toContain('aria-label="删除目标 Example Node A"')
    expect(html).toContain('admin-row-action is-icon')
    expect(html).toContain('admin-row-action is-icon is-danger')
    expect(html).not.toContain('停用目标')
    expect(html).not.toContain('全节点启用')
    expect(html).not.toContain('全节点停用')
    expect(html).not.toContain('上移')
    expect(html).not.toContain('下移')
    expect(html.indexOf('Example ICMP')).toBeLessThan(html.indexOf('Example Node A'))
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
    expect(html).toContain('1 / 1 服务器启用')
    expect(html).not.toContain('8.8.8.8:')
  })

  it('renders HTTP GET monitor targets without requiring a port', () => {
    const html = renderAdmin('targets')

    expect(html).toContain('Zeno Health HTTP')
    expect(html).not.toContain('HTTP GET')
    expect(html).toContain('https://example.com/health')
    expect(html).not.toContain('2 次 / 1500ms / 60s')
    expect(html).toContain('1 / 1 服务器启用')
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
