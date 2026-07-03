import { type FormEvent, type ReactNode, useEffect, useState } from 'react'
import { createAdminNode, createAdminNotificationChannel, createAdminProbeTarget, deleteAdminNotificationChannel, deleteAdminProbeTarget, fetchAdminNodes, fetchAdminNotificationChannels, fetchAdminNotificationDeliveries, fetchAdminNotificationTypes, fetchAdminProbeTargets, fetchNodeLatency, fetchNodeState, fetchSummary, requestAdminNodeInstallCommand, testAdminNotificationChannel, updateAdminNode, updateAdminNotificationChannel, updateAdminNotificationType, updateAdminProbeTarget, type AdminNodeCreateInput, type AdminNodeUpdateInput, type AdminNotificationChannelCreateInput, type AdminNotificationChannelUpdateInput, type AdminProbeTargetInput, type AdminProbeTargetUpdateInput, type NodeLatencyData, type NodeStateData, type SummaryData } from './api/client'
import { LatencyDetail } from './components/LatencyDetail'
import { ServerCard } from './components/ServerCard'
import { startLiveRefresh } from './lib/liveRefresh'
import { nodePath, parseDashboardRoute, type DashboardRoute } from './lib/route'
import type { AdminNode, AdminNotificationChannel, AdminNotificationDelivery, AdminNotificationType, AdminProbeTarget, ProbeType } from './types'

type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; data: SummaryData }
  | { kind: 'error'; message: string }

type LatencyLoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; data: NodeLatencyData }
  | { kind: 'error'; message: string }

type StateHistoryLoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; data: NodeStateData }
  | { kind: 'error'; message: string }

type AdminLoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; nodes: AdminNode[]; targets: AdminProbeTarget[]; notificationChannels: AdminNotificationChannel[]; notificationTypes: AdminNotificationType[]; notificationDeliveries: AdminNotificationDelivery[] }
  | { kind: 'error'; message: string }

type AdminSection = 'overview' | 'nodes' | 'targets' | 'notifications'
type AdminTargetSort = 'name' | 'status' | 'type' | 'assignments'

function sum(values: Array<number | null | undefined>): number {
  return values.reduce<number>((total, value) => total + (value ?? 0), 0)
}

function compactBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  const digits = unit === 0 ? 0 : 2
  return `${size.toFixed(digits)} ${units[unit]}`
}

function compactRate(value: number): string {
  return `${compactBytes(value)}/s`
}

export function App() {
  const [state, setState] = useState<LoadState>({ kind: 'loading' })
  const [route, setRoute] = useState<DashboardRoute>(() => parseDashboardRoute(window.location.pathname))
  const [latencyRange, setLatencyRange] = useState('1d')
  const [latencyState, setLatencyState] = useState<LatencyLoadState>({ kind: 'idle' })
  const [stateHistoryState, setStateHistoryState] = useState<StateHistoryLoadState>({ kind: 'idle' })
  const [adminToken, setAdminToken] = useState(() => window.sessionStorage.getItem('zeno_admin_token') ?? '')
  const [adminState, setAdminState] = useState<AdminLoadState>({ kind: 'idle' })

  useEffect(() => {
    let cancelled = false
    const loadSummary = () => {
      fetchSummary()
        .then((data) => {
          if (!cancelled) setState({ kind: 'ready', data })
        })
        .catch((error: unknown) => {
          if (!cancelled) setState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
        })
    }

    loadSummary()
    const stopRefresh = startLiveRefresh(loadSummary)
    return () => {
      cancelled = true
      stopRefresh()
    }
  }, [])

  useEffect(() => {
    const handlePopState = () => setRoute(parseDashboardRoute(window.location.pathname))
    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
  }, [])

  useEffect(() => {
    if (route.kind !== 'node') {
      setLatencyState({ kind: 'idle' })
      return
    }

    let cancelled = false
    let loadedOnce = false
    const loadLatency = () => {
      if (!loadedOnce) setLatencyState({ kind: 'loading' })
      fetchNodeLatency(route.nodeId, latencyRange)
        .then((data) => {
          loadedOnce = true
          if (!cancelled) setLatencyState({ kind: 'ready', data })
        })
        .catch((error: unknown) => {
          loadedOnce = true
          if (!cancelled) setLatencyState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
        })
    }

    loadLatency()
    const stopRefresh = startLiveRefresh(loadLatency)
    return () => {
      cancelled = true
      stopRefresh()
    }
  }, [route, latencyRange])

  useEffect(() => {
    if (route.kind !== 'node') {
      setStateHistoryState({ kind: 'idle' })
      return
    }

    let cancelled = false
    let loadedOnce = false
    const loadStateHistory = () => {
      if (!loadedOnce) setStateHistoryState({ kind: 'loading' })
      fetchNodeState(route.nodeId, latencyRange)
        .then((data) => {
          loadedOnce = true
          if (!cancelled) setStateHistoryState({ kind: 'ready', data })
        })
        .catch((error: unknown) => {
          loadedOnce = true
          if (!cancelled) setStateHistoryState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
        })
    }

    loadStateHistory()
    const stopRefresh = startLiveRefresh(loadStateHistory)
    return () => {
      cancelled = true
      stopRefresh()
    }
  }, [route, latencyRange])

  useEffect(() => {
    if (route.kind !== 'admin') return
    if (adminToken === '') {
      setAdminState({ kind: 'idle' })
      return
    }

    let cancelled = false
    let loadedOnce = false
    const loadAdminNodes = () => {
      if (!loadedOnce) setAdminState({ kind: 'loading' })
      Promise.all([fetchAdminNodes(adminToken), fetchAdminProbeTargets(adminToken), fetchAdminNotificationChannels(adminToken), fetchAdminNotificationTypes(adminToken), fetchAdminNotificationDeliveries(adminToken)])
        .then(([nodesData, targetsData, channelsData, typesData, deliveriesData]) => {
          loadedOnce = true
          if (!cancelled) setAdminState({ kind: 'ready', nodes: nodesData.nodes, targets: targetsData.targets, notificationChannels: channelsData.channels, notificationTypes: typesData.types, notificationDeliveries: deliveriesData.deliveries })
        })
        .catch((error: unknown) => {
          loadedOnce = true
          if (!cancelled) setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
        })
    }

    loadAdminNodes()
    const stopRefresh = startLiveRefresh(loadAdminNodes)
    return () => {
      cancelled = true
      stopRefresh()
    }
  }, [route, adminToken])

  const submitAdminToken = (token: string) => {
    const trimmed = token.trim()
    if (trimmed === '') return
    window.sessionStorage.setItem('zeno_admin_token', trimmed)
    setAdminToken(trimmed)
  }

  const clearAdminToken = () => {
    window.sessionStorage.removeItem('zeno_admin_token')
    setAdminToken('')
    setAdminState({ kind: 'idle' })
  }

  const refreshAdminNodes = () => {
    if (adminToken === '') return
    setAdminState({ kind: 'loading' })
    Promise.all([fetchAdminNodes(adminToken), fetchAdminProbeTargets(adminToken), fetchAdminNotificationChannels(adminToken), fetchAdminNotificationTypes(adminToken), fetchAdminNotificationDeliveries(adminToken)])
      .then(([nodesData, targetsData, channelsData, typesData, deliveriesData]) => setAdminState({ kind: 'ready', nodes: nodesData.nodes, targets: targetsData.targets, notificationChannels: channelsData.channels, notificationTypes: typesData.types, notificationDeliveries: deliveriesData.deliveries }))
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const createAdminNodeDetails = (input: AdminNodeCreateInput) => {
    if (adminToken === '') return
    createAdminNode(adminToken, input)
      .then((createdNode) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { kind: 'ready', nodes: [...current.nodes, createdNode], targets: current.targets, notificationChannels: current.notificationChannels, notificationTypes: current.notificationTypes, notificationDeliveries: current.notificationDeliveries }
          }
          return { kind: 'ready', nodes: [createdNode], targets: [], notificationChannels: [], notificationTypes: [], notificationDeliveries: [] }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const requestAdminInstallCommand = (nodeId: string): Promise<string> => {
    if (adminToken === '') return Promise.reject(new Error('missing admin token'))
    return requestAdminNodeInstallCommand(adminToken, nodeId).then((result) => result.command)
  }

  const updateAdminNodeDetails = (nodeId: string, input: AdminNodeUpdateInput) => {
    if (adminToken === '') return
    updateAdminNode(adminToken, nodeId, input)
      .then((updatedNode) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { kind: 'ready', nodes: current.nodes.map((node) => node.id === updatedNode.id ? updatedNode : node), targets: current.targets, notificationChannels: current.notificationChannels, notificationTypes: current.notificationTypes, notificationDeliveries: current.notificationDeliveries }
          }
          return { kind: 'ready', nodes: [updatedNode], targets: [], notificationChannels: [], notificationTypes: [], notificationDeliveries: [] }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const createAdminProbeTargetDetails = (input: AdminProbeTargetInput) => {
    if (adminToken === '') return
    createAdminProbeTarget(adminToken, input)
      .then((createdTarget) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { kind: 'ready', nodes: current.nodes, targets: [...current.targets, createdTarget], notificationChannels: current.notificationChannels, notificationTypes: current.notificationTypes, notificationDeliveries: current.notificationDeliveries }
          }
          return { kind: 'ready', nodes: [], targets: [createdTarget], notificationChannels: [], notificationTypes: [], notificationDeliveries: [] }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const updateAdminProbeTargetDetails = (targetId: string, input: AdminProbeTargetUpdateInput) => {
    if (adminToken === '') return
    updateAdminProbeTarget(adminToken, targetId, input)
      .then((updatedTarget) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { kind: 'ready', nodes: current.nodes, targets: current.targets.map((target) => target.id === updatedTarget.id ? updatedTarget : target), notificationChannels: current.notificationChannels, notificationTypes: current.notificationTypes, notificationDeliveries: current.notificationDeliveries }
          }
          return { kind: 'ready', nodes: [], targets: [updatedTarget], notificationChannels: [], notificationTypes: [], notificationDeliveries: [] }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const deleteAdminProbeTargetDetails = (targetId: string) => {
    if (adminToken === '') return
    deleteAdminProbeTarget(adminToken, targetId)
      .then(() => {
        setAdminState((current) => {
          if (current.kind !== 'ready') return current
          return { ...current, targets: current.targets.filter((target) => target.id !== targetId) }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }


  const createAdminNotificationChannelDetails = (input: AdminNotificationChannelCreateInput) => {
    if (adminToken === '') return
    createAdminNotificationChannel(adminToken, input)
      .then((createdChannel) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { kind: 'ready', nodes: current.nodes, targets: current.targets, notificationChannels: [...current.notificationChannels, createdChannel], notificationTypes: current.notificationTypes, notificationDeliveries: current.notificationDeliveries }
          }
          return { kind: 'ready', nodes: [], targets: [], notificationChannels: [createdChannel], notificationTypes: [], notificationDeliveries: [] }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const updateAdminNotificationChannelDetails = (channelId: string, input: AdminNotificationChannelUpdateInput) => {
    if (adminToken === '') return
    updateAdminNotificationChannel(adminToken, channelId, input)
      .then((updatedChannel) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { kind: 'ready', nodes: current.nodes, targets: current.targets, notificationChannels: current.notificationChannels.map((channel) => channel.id === updatedChannel.id ? updatedChannel : channel), notificationTypes: current.notificationTypes, notificationDeliveries: current.notificationDeliveries }
          }
          return { kind: 'ready', nodes: [], targets: [], notificationChannels: [updatedChannel], notificationTypes: [], notificationDeliveries: [] }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const deleteAdminNotificationChannelDetails = (channelId: string) => {
    if (adminToken === '') return
    deleteAdminNotificationChannel(adminToken, channelId)
      .then(() => {
        setAdminState((current) => {
          if (current.kind !== 'ready') return current
          return { ...current, notificationChannels: current.notificationChannels.filter((channel) => channel.id !== channelId) }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const testAdminNotificationChannelDetails = (channelId: string) => {
    if (adminToken === '') return
    testAdminNotificationChannel(adminToken, channelId)
      .then((delivery) => {
        setAdminState((current) => {
          if (current.kind !== 'ready') return current
          return { ...current, notificationDeliveries: [delivery, ...current.notificationDeliveries].slice(0, 50) }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const updateAdminNotificationTypeDetails = (eventType: string, enabled: boolean) => {
    if (adminToken === '') return
    updateAdminNotificationType(adminToken, eventType, enabled)
      .then((updatedType) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { kind: 'ready', nodes: current.nodes, targets: current.targets, notificationChannels: current.notificationChannels, notificationTypes: current.notificationTypes.map((notificationType) => notificationType.eventType === updatedType.eventType ? updatedType : notificationType), notificationDeliveries: current.notificationDeliveries }
          }
          return { kind: 'ready', nodes: [], targets: [], notificationChannels: [], notificationTypes: [updatedType], notificationDeliveries: [] }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const navigateHome = () => {
    window.history.pushState(null, '', '/')
    setRoute({ kind: 'home' })
  }

  const navigateAdmin = () => {
    window.history.pushState(null, '', '/dashboard')
    setRoute({ kind: 'admin' })
  }

  const navigateNode = (nodeId: string) => {
    window.history.pushState(null, '', nodePath(nodeId))
    setLatencyRange('1d')
    setRoute({ kind: 'node', nodeId })
  }

  const nodes = state.kind === 'ready' ? state.data.nodes : []
  const selectedNode = route.kind === 'node' ? nodes.find((node) => node.id === route.nodeId) : undefined
  const totalCount = nodes.length
  const onlineCount = nodes.filter((node) => node.status === 'online').length
  const offlineCount = nodes.filter((node) => node.status === 'offline').length
  const totalUp = sum(nodes.map((node) => node.netOutTotalBytes))
  const totalDown = sum(nodes.map((node) => node.netInTotalBytes))
  const upSpeed = sum(nodes.map((node) => node.netOutSpeedBps))
  const downSpeed = sum(nodes.map((node) => node.netInSpeedBps))

  return (
    <main className="kulin-shell">
      {route.kind === 'node' && <DashboardHeader onHome={navigateHome} onAdmin={navigateAdmin} />}

      {route.kind === 'admin' && (
        <AdminDashboard
          onHome={navigateHome}
          hasAdminToken={adminToken !== ''}
          adminState={adminState}
          onAdminTokenSubmit={submitAdminToken}
          onAdminTokenClear={clearAdminToken}
          onAdminRefresh={refreshAdminNodes}
          onAdminNodeCreate={createAdminNodeDetails}
          onAdminNodeUpdate={updateAdminNodeDetails}
          onAdminInstallCommand={requestAdminInstallCommand}
          onAdminProbeTargetCreate={createAdminProbeTargetDetails}
          onAdminProbeTargetUpdate={updateAdminProbeTargetDetails}
          onAdminProbeTargetDelete={deleteAdminProbeTargetDetails}
          onAdminNotificationChannelCreate={createAdminNotificationChannelDetails}
          onAdminNotificationChannelUpdate={updateAdminNotificationChannelDetails}
          onAdminNotificationChannelDelete={deleteAdminNotificationChannelDetails}
          onAdminNotificationChannelTest={testAdminNotificationChannelDetails}
          onAdminNotificationTypeUpdate={updateAdminNotificationTypeDetails}
        />
      )}

      {route.kind !== 'admin' && state.kind === 'loading' && <section className="state-panel">正在读取 Controller API…</section>}
      {route.kind !== 'admin' && state.kind === 'error' && <section className="state-panel is-error">API 读取失败：{state.message}</section>}

      {state.kind === 'ready' && route.kind === 'node' && selectedNode && (
        <LatencyDetail
          node={selectedNode}
          points={latencyState.kind === 'ready' ? latencyState.data.points : []}
          statePoints={stateHistoryState.kind === 'ready' ? stateHistoryState.data.points : []}
          range={latencyRange}
          loading={latencyState.kind === 'loading'}
          error={latencyState.kind === 'error' ? latencyState.message : undefined}
          stateLoading={stateHistoryState.kind === 'loading'}
          stateError={stateHistoryState.kind === 'error' ? stateHistoryState.message : undefined}
          onBack={navigateHome}
          onRangeChange={setLatencyRange}
        />
      )}

      {state.kind === 'ready' && route.kind === 'node' && !selectedNode && (
        <section className="state-panel is-error">没有找到这台服务器：{route.nodeId}</section>
      )}

      {state.kind === 'ready' && route.kind === 'home' && (
        <div className="kulin-container">
          <HomeTopPanel
            totalCount={totalCount}
            onlineCount={onlineCount}
            offlineCount={offlineCount}
            totalUp={totalUp}
            totalDown={totalDown}
            upSpeed={upSpeed}
            downSpeed={downSpeed}
            onHome={navigateHome}
            onAdmin={navigateAdmin}
          />

          <section className="server-card-list" aria-label="server cards">
            {nodes.map((node) => <ServerCard key={node.id} node={node} onOpen={navigateNode} />)}
          </section>
        </div>
      )}
    </main>
  )
}

interface HomeOverviewPanelProps {
  totalCount: number
  onlineCount: number
  offlineCount: number
  totalUp: number
  totalDown: number
  upSpeed: number
  downSpeed: number
}

interface DashboardHeaderProps {
  onHome: () => void
  onAdmin: () => void
  adminLabel?: string
}

interface HomeTopPanelProps extends HomeOverviewPanelProps {
  onHome: () => void
  onAdmin: () => void
}

export function HomeTopPanel({ onHome, onAdmin, ...overview }: HomeTopPanelProps) {
  return (
    <section className="home-top-card" aria-label="homepage control panel">
      <DashboardHeader onHome={onHome} onAdmin={onAdmin} />
      <HomeOverviewPanel {...overview} />
    </section>
  )
}

function DashboardHeader({ onHome, onAdmin, adminLabel = '后台' }: DashboardHeaderProps) {
  return (
    <header className="kulin-nav">
      <button className="brand" type="button" onClick={onHome}>
        <span className="brand-logo"><img src="/assets/logo/id.png" alt="apple-touch-icon" /></span>
        <span>Zeno</span>
      </button>
      <nav className="nav-actions" aria-label="dashboard actions">
        <button className="login-link" type="button" onClick={onAdmin}>{adminLabel}</button>
        <button className="nav-icon-button is-solid" type="button" aria-label="language"><MapIcon /></button>
        <button className="nav-icon-button" type="button" aria-label="切换主题"><SunIcon /><span className="sr-only">切换主题</span></button>
        <button className="nav-icon-button" type="button" aria-label="background"><ImageMinusIcon /></button>
      </nav>
    </header>
  )
}

interface AdminDashboardProps {
  onHome: () => void
  hasAdminToken?: boolean
  adminState?: AdminLoadState
  initialSection?: AdminSection
  onAdminTokenSubmit?: (token: string) => void
  onAdminTokenClear?: () => void
  onAdminRefresh?: () => void
  onAdminNodeCreate?: (input: AdminNodeCreateInput) => void
  onAdminNodeUpdate?: (nodeId: string, input: AdminNodeUpdateInput) => void
  onAdminInstallCommand?: (nodeId: string) => Promise<string>
  onAdminProbeTargetCreate?: (input: AdminProbeTargetInput) => void
  onAdminProbeTargetUpdate?: (targetId: string, input: AdminProbeTargetUpdateInput) => void
  onAdminProbeTargetDelete?: (targetId: string) => void
  onAdminNotificationChannelCreate?: (input: AdminNotificationChannelCreateInput) => void
  onAdminNotificationChannelUpdate?: (channelId: string, input: AdminNotificationChannelUpdateInput) => void
  onAdminNotificationChannelDelete?: (channelId: string) => void
  onAdminNotificationChannelTest?: (channelId: string) => void
  onAdminNotificationTypeUpdate?: (eventType: string, enabled: boolean) => void
}

export function AdminDashboard({
  onHome,
  hasAdminToken = false,
  adminState = { kind: 'idle' },
  initialSection = 'overview',
  onAdminTokenSubmit = () => {},
  onAdminTokenClear = () => {},
  onAdminRefresh = () => {},
  onAdminNodeCreate = () => {},
  onAdminNodeUpdate = () => {},
  onAdminInstallCommand = () => Promise.reject(new Error('install command unavailable')),
  onAdminProbeTargetCreate = () => {},
  onAdminProbeTargetUpdate = () => {},
  onAdminProbeTargetDelete = () => {},
  onAdminNotificationChannelCreate = () => {},
  onAdminNotificationChannelUpdate = () => {},
  onAdminNotificationChannelDelete = () => {},
  onAdminNotificationChannelTest = () => {},
  onAdminNotificationTypeUpdate = () => {},
}: AdminDashboardProps) {
  const [activeSection, setActiveSection] = useState<AdminSection>(initialSection)

  const handleTokenSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const formData = new FormData(form)
    const username = String(formData.get('admin-username') ?? '').trim()
    const password = String(formData.get('admin-password') ?? '').trim()
    if (username !== 'admin' || password === '') return
    onAdminTokenSubmit(password)
    form.reset()
  }

  const nodeCount = adminState.kind === 'ready' ? adminState.nodes.length : 0
  const targetCount = adminState.kind === 'ready' ? adminState.targets.length : 0
  const onlineNodeCount = adminState.kind === 'ready' ? adminState.nodes.filter((node) => node.status === 'online').length : 0
  const enabledTargetCount = adminState.kind === 'ready' ? adminState.targets.filter((target) => target.enabled).length : 0

  return (
    <div className="kulin-container admin-container">
      <section className="home-top-card admin-panel" aria-label="admin dashboard">
        <DashboardHeader onHome={onHome} onAdmin={onHome} adminLabel="前台" />
        <div className="admin-hero">
          <p className="eyebrow">Zeno 后台</p>
          <h2>控制台</h2>
          <p>服务器、延迟监控和通知拆成独立导航；列表只保留关键字段，编辑放进弹窗里处理。</p>
        </div>

        {!hasAdminToken && (
          <>
            <div className="admin-action-grid" aria-label="admin modules">
              <article className="admin-action-card">
                <p>服务器</p>
                <strong>列表 / 弹窗编辑</strong>
              </article>
              <article className="admin-action-card">
                <p>延迟监控</p>
                <strong>目标 / 节点分配</strong>
              </article>
              <article className="admin-action-card">
                <p>通知渠道</p>
                <strong>Telegram / Webhook</strong>
              </article>
              <article className="admin-action-card">
                <p>通知类型</p>
                <strong>上线 / 离线 / 异常</strong>
              </article>
            </div>
            <form className="admin-login-card" aria-label="admin login form" onSubmit={handleTokenSubmit}>
              <div>
                <p>后台登录</p>
                <strong>默认账号：admin / admin</strong>
              </div>
              <label>
                <span>账号</span>
                <input name="admin-username" autoComplete="username" placeholder="admin" aria-label="后台账号" />
              </label>
              <label>
                <span>密码</span>
                <input name="admin-password" type="password" autoComplete="current-password" placeholder="admin" aria-label="后台密码" />
              </label>
              <button type="submit">登录后台</button>
            </form>
          </>
        )}

        {hasAdminToken && (
          <>
            <div className="admin-toolbar">
              <AdminSectionNav
                activeSection={activeSection}
                onSectionChange={setActiveSection}
                nodeCount={nodeCount}
                targetCount={targetCount}
              />
              <div className="admin-section-actions">
                <button type="button" onClick={onAdminRefresh}>刷新</button>
                <button type="button" onClick={onAdminTokenClear}>退出</button>
              </div>
            </div>

            {adminState.kind === 'loading' && <div className="admin-state-card">正在读取 Admin API…</div>}
            {adminState.kind === 'error' && <div className="admin-state-card is-error">Admin API 读取失败：{adminState.message}</div>}

            {adminState.kind === 'ready' && activeSection === 'overview' && (
              <AdminOverviewPanel
                nodeCount={nodeCount}
                onlineNodeCount={onlineNodeCount}
                targetCount={targetCount}
                enabledTargetCount={enabledTargetCount}
              />
            )}

            {adminState.kind === 'ready' && activeSection === 'nodes' && (
              <AdminNodeSection
                nodes={adminState.nodes}
                onCreate={onAdminNodeCreate}
                onUpdate={onAdminNodeUpdate}
                onInstallCommand={onAdminInstallCommand}
              />
            )}

            {adminState.kind === 'ready' && activeSection === 'targets' && (
              <AdminTargetSection
                targets={adminState.targets}
                nodes={adminState.nodes}
                onCreate={onAdminProbeTargetCreate}
                onUpdate={onAdminProbeTargetUpdate}
                onDelete={onAdminProbeTargetDelete}
              />
            )}

            {adminState.kind === 'ready' && activeSection === 'notifications' && (
              <AdminNotificationsSection
                channels={adminState.notificationChannels}
                types={adminState.notificationTypes}
                deliveries={adminState.notificationDeliveries}
                onChannelCreate={onAdminNotificationChannelCreate}
                onChannelUpdate={onAdminNotificationChannelUpdate}
                onChannelDelete={onAdminNotificationChannelDelete}
                onChannelTest={onAdminNotificationChannelTest}
                onTypeUpdate={onAdminNotificationTypeUpdate}
              />
            )}
          </>
        )}
      </section>
    </div>
  )
}

function AdminSectionNav({ activeSection, onSectionChange, nodeCount, targetCount }: { activeSection: AdminSection; onSectionChange: (section: AdminSection) => void; nodeCount: number; targetCount: number }) {
  const sections: Array<{ id: AdminSection; label: string; meta: string }> = [
    { id: 'overview', label: '概览', meta: 'Summary' },
    { id: 'nodes', label: '服务器', meta: `${nodeCount} 台` },
    { id: 'targets', label: '延迟监控', meta: `${targetCount} 个目标` },
    { id: 'notifications', label: '通知', meta: 'Channels' },
  ]

  return (
    <nav className="admin-section-nav" aria-label="后台导航">
      {sections.map((section) => (
        <button
          key={section.id}
          type="button"
          data-active={activeSection === section.id}
          onClick={() => onSectionChange(section.id)}
        >
          <span>{section.label}</span>
          <em>{section.meta}</em>
        </button>
      ))}
    </nav>
  )
}

function AdminOverviewPanel({ nodeCount, onlineNodeCount, targetCount, enabledTargetCount }: { nodeCount: number; onlineNodeCount: number; targetCount: number; enabledTargetCount: number }) {
  return (
    <section className="admin-overview-panel" aria-label="admin overview">
      <div className="admin-action-grid" aria-label="admin modules">
        <article className="admin-action-card">
          <p>服务器</p>
          <strong>{onlineNodeCount} / {nodeCount} 在线</strong>
        </article>
        <article className="admin-action-card">
          <p>延迟监控</p>
          <strong>{enabledTargetCount} / {targetCount} 启用</strong>
        </article>
        <article className="admin-action-card">
          <p>通知渠道</p>
          <strong>Telegram / Webhook</strong>
        </article>
        <article className="admin-action-card">
          <p>通知类型</p>
          <strong>上线 / 离线 / 异常</strong>
        </article>
      </div>
      <p className="admin-overview-note">从上方导航进入具体模块；列表页只显示关键状态，所有编辑动作都在弹窗中完成。</p>
    </section>
  )
}

function AdminNodeSection({ nodes, onCreate, onUpdate, onInstallCommand }: { nodes: AdminNode[]; onCreate: (input: AdminNodeCreateInput) => void; onUpdate: (nodeId: string, input: AdminNodeUpdateInput) => void; onInstallCommand: (nodeId: string) => Promise<string> }) {
  const [creatingNode, setCreatingNode] = useState(false)
  const [editingNodeId, setEditingNodeId] = useState<string | null>(null)
  const editingNode = editingNodeId ? nodes.find((node) => node.id === editingNodeId) : undefined

  return (
    <section className="admin-node-section admin-workspace-panel" aria-label="admin node list">
      <header className="admin-section-heading">
        <div>
          <p className="eyebrow">Servers</p>
          <h3>服务器列表</h3>
        </div>
        <button className="admin-primary-action" type="button" onClick={() => setCreatingNode(true)}>添加服务器</button>
      </header>

      {nodes.length === 0 && <div className="admin-state-card">还没有节点。</div>}
      {nodes.length > 0 && <AdminNodeList nodes={nodes} onEdit={setEditingNodeId} />}

      {creatingNode && (
        <AdminNodeCreateModal
          onClose={() => setCreatingNode(false)}
          onCreate={(input) => {
            onCreate(input)
            setCreatingNode(false)
          }}
        />
      )}

      {editingNode && (
        <AdminNodeEditModal
          key={editingNode.id}
          node={editingNode}
          onClose={() => setEditingNodeId(null)}
          onUpdate={(nodeId, input) => {
            onUpdate(nodeId, input)
            setEditingNodeId(null)
          }}
          onInstallCommand={onInstallCommand}
        />
      )}
    </section>
  )
}

function AdminNodeList({ nodes, onEdit }: { nodes: AdminNode[]; onEdit: (nodeId: string) => void }) {
  return (
    <div className="admin-list" role="list" aria-label="服务器列表">
      <div className="admin-list-head" aria-hidden="true">
        <span>服务器</span>
        <span>状态</span>
        <span>系统</span>
        <span>最近在线</span>
        <span>Agent</span>
        <span>操作</span>
      </div>
      {nodes.map((node) => (
        <article className="admin-list-row" role="listitem" key={node.id}>
          <div className="admin-list-main">
            <strong>{node.displayName}</strong>
            <small>{node.id}{node.region ? ` · ${node.region}` : ''}</small>
          </div>
          <span className={`admin-node-status status-${node.disabled ? 'disabled' : node.status}`}>{node.disabled ? 'disabled' : node.status}</span>
          <span>{formatAdminSystem(node)}</span>
          <span>{formatAdminDate(node.lastSeenAt)}</span>
          <span>{node.agentVersion || '—'}</span>
          <button className="admin-row-action" type="button" onClick={() => onEdit(node.id)}>编辑服务器</button>
        </article>
      ))}
    </div>
  )
}

function AdminNodeCreateModal({ onCreate, onClose }: { onCreate: (input: AdminNodeCreateInput) => void; onClose: () => void }) {
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const formData = new FormData(form)
    const displayName = String(formData.get('new-display-name') ?? '').trim()
    if (displayName === '') return
    onCreate({
      id: String(formData.get('new-node-id') ?? '').trim() || undefined,
      displayName,
      countryCode: String(formData.get('new-country-code') ?? ''),
      region: String(formData.get('new-region') ?? ''),
      monthlyQuotaBytes: parseQuotaGigabytes(String(formData.get('new-monthly-quota-gb') ?? '')),
    })
  }

  return (
    <AdminModal title="添加服务器" eyebrow="Servers" onClose={onClose}>
      <form className="admin-node-create-form admin-node-edit-form" aria-label="添加服务器" onSubmit={handleSubmit}>
        <label>
          <span>服务器名称</span>
          <input name="new-display-name" autoComplete="off" placeholder="New Server" />
        </label>
        <label>
          <span>节点 ID（可选）</span>
          <input name="new-node-id" autoComplete="off" placeholder="自动生成" />
        </label>
        <label>
          <span>国家</span>
          <input name="new-country-code" autoComplete="off" placeholder="HK" />
        </label>
        <label>
          <span>地区</span>
          <input name="new-region" autoComplete="off" placeholder="Hong Kong" />
        </label>
        <label>
          <span>月配额 GB</span>
          <input name="new-monthly-quota-gb" type="number" min="0" step="0.01" />
        </label>
        <button type="submit">添加服务器</button>
      </form>
    </AdminModal>
  )
}

function AdminNodeEditModal({ node, onUpdate, onInstallCommand, onClose }: { node: AdminNode; onUpdate: (nodeId: string, input: AdminNodeUpdateInput) => void; onInstallCommand: (nodeId: string) => Promise<string>; onClose: () => void }) {
  const [installCommandState, setInstallCommandState] = useState<{ kind: 'idle' } | { kind: 'loading' } | { kind: 'ready'; command: string } | { kind: 'error'; message: string }>({ kind: 'idle' })

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    onUpdate(node.id, {
      displayName: String(formData.get('display-name') ?? ''),
      countryCode: String(formData.get('country-code') ?? ''),
      region: String(formData.get('region') ?? ''),
      monthlyQuotaBytes: parseQuotaGigabytes(String(formData.get('monthly-quota-gb') ?? '')),
      disabled: formData.get('disabled') === 'on',
    })
  }

  const handleInstallCommand = () => {
    setInstallCommandState({ kind: 'loading' })
    onInstallCommand(node.id)
      .then((command) => setInstallCommandState({ kind: 'ready', command }))
      .catch((error: unknown) => setInstallCommandState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  return (
    <AdminModal title={`编辑服务器 · ${node.displayName}`} eyebrow={node.id} onClose={onClose}>
      <dl className="admin-modal-summary">
        <div><dt>状态</dt><dd>{node.disabled ? 'disabled' : node.status}</dd></div>
        <div><dt>系统</dt><dd>{formatAdminSystem(node)}</dd></div>
        <div><dt>资源</dt><dd>{formatAdminResources(node)}</dd></div>
        <div><dt>最近在线</dt><dd>{formatAdminDate(node.lastSeenAt)}</dd></div>
      </dl>
      <form className="admin-node-edit-form" aria-label={`${node.displayName} 节点编辑`} onSubmit={handleSubmit}>
        <label>
          <span>显示名</span>
          <input name="display-name" defaultValue={node.displayName} autoComplete="off" />
        </label>
        <label>
          <span>国家</span>
          <input name="country-code" defaultValue={node.countryCode ?? ''} autoComplete="off" />
        </label>
        <label>
          <span>地区</span>
          <input name="region" defaultValue={node.region ?? ''} autoComplete="off" />
        </label>
        <label>
          <span>月配额 GB</span>
          <input name="monthly-quota-gb" type="number" min="0" step="0.01" defaultValue={formatQuotaGigabytes(node.monthlyQuotaBytes)} />
        </label>
        <label className="admin-node-toggle">
          <input name="disabled" type="checkbox" defaultChecked={node.disabled} />
          <span>禁用节点</span>
        </label>
        <div className="admin-modal-actions">
          <button type="submit">保存服务器</button>
          <button type="button" onClick={handleInstallCommand} disabled={installCommandState.kind === 'loading'}>{installCommandState.kind === 'loading' ? '生成中…' : '获取安装命令'}</button>
        </div>
      </form>
      {installCommandState.kind === 'ready' && (
        <textarea className="admin-install-command" aria-label={`${node.displayName} Agent 安装命令`} readOnly value={installCommandState.command} />
      )}
      {installCommandState.kind === 'error' && <div className="admin-install-error">安装命令生成失败：{installCommandState.message}</div>}
    </AdminModal>
  )
}

function AdminTargetSection({ targets, nodes, onCreate, onUpdate, onDelete }: { targets: AdminProbeTarget[]; nodes: AdminNode[]; onCreate: (input: AdminProbeTargetInput) => void; onUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => void; onDelete: (targetId: string) => void }) {
  const [creatingTarget, setCreatingTarget] = useState(false)
  const [editingTargetId, setEditingTargetId] = useState<string | null>(null)
  const [targetSort, setTargetSort] = useState<AdminTargetSort>('name')
  const editingTarget = editingTargetId ? targets.find((target) => target.id === editingTargetId) : undefined
  const sortedTargets = sortAdminProbeTargets(targets, targetSort)

  return (
    <section className="admin-target-section admin-workspace-panel" aria-label="admin probe target list">
      <header className="admin-section-heading">
        <div>
          <p className="eyebrow">Latency</p>
          <h3>延迟监控</h3>
        </div>
        <div className="admin-section-actions">
          <label className="admin-sort-control">
            <span>排序</span>
            <select name="target-sort" value={targetSort} onChange={(event) => setTargetSort(event.currentTarget.value as AdminTargetSort)}>
              <option value="name">按名称排序</option>
              <option value="status">按启用状态排序</option>
              <option value="type">按类型排序</option>
              <option value="assignments">按节点分配排序</option>
            </select>
          </label>
          <button className="admin-primary-action" type="button" onClick={() => setCreatingTarget(true)}>添加目标</button>
        </div>
      </header>

      {targets.length === 0 && <div className="admin-state-card">还没有探针目标。</div>}
      {targets.length > 0 && <AdminTargetList targets={sortedTargets} nodes={nodes} onEdit={setEditingTargetId} onUpdate={onUpdate} onDelete={onDelete} />}

      {creatingTarget && (
        <AdminTargetCreateModal
          onClose={() => setCreatingTarget(false)}
          onCreate={(input) => {
            onCreate(input)
            setCreatingTarget(false)
          }}
        />
      )}

      {editingTarget && (
        <AdminTargetEditModal
          key={editingTarget.id}
          target={editingTarget}
          nodes={nodes}
          onClose={() => setEditingTargetId(null)}
          onUpdate={(targetId, input) => {
            onUpdate(targetId, input)
            setEditingTargetId(null)
          }}
        />
      )}
    </section>
  )
}

function AdminTargetList({ targets, nodes, onEdit, onUpdate, onDelete }: { targets: AdminProbeTarget[]; nodes: AdminNode[]; onEdit: (targetId: string) => void; onUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => void; onDelete: (targetId: string) => void }) {
  const confirmDelete = (target: AdminProbeTarget) => {
    const ok = typeof window === 'undefined' ? true : window.confirm(`确认删除延迟监控目标「${target.name}」？`)
    if (ok) onDelete(target.id)
  }
  const updateAllAssignments = (target: AdminProbeTarget, enabled: boolean) => {
    const assignments = targetAssignmentRows(target, nodes).map((assignment) => ({ nodeId: assignment.nodeId, enabled }))
    if (assignments.length > 0) onUpdate(target.id, { assignments })
  }

  return (
    <div className="admin-list admin-target-list" role="list" aria-label="延迟监控目标列表">
      <div className="admin-list-head" aria-hidden="true">
        <span>目标</span>
        <span>状态</span>
        <span>地址</span>
        <span>参数</span>
        <span>节点</span>
        <span>操作</span>
      </div>
      {targets.map((target) => (
        <article className="admin-list-row" role="listitem" key={target.id}>
          <div className="admin-list-main">
            <strong>{target.name}</strong>
            <small>{target.id}</small>
          </div>
          <span className={`admin-node-status status-${target.enabled ? 'online' : 'disabled'}`}>{target.enabled ? 'enabled' : 'disabled'}</span>
          <span>{formatTargetEndpoint(target)}</span>
          <span>{formatTargetTypeLabel(target.type)} · {target.count} 次 / {target.timeoutMs}ms / {target.intervalSec}s</span>
          <span>{formatTargetAssignmentSummary(target)}</span>
          <div className="admin-row-actions">
            <button className="admin-row-action" type="button" onClick={() => onEdit(target.id)}>编辑目标</button>
            <button className="admin-row-action" type="button" onClick={() => onUpdate(target.id, { enabled: !target.enabled })}>
              {target.enabled ? '停用目标' : '启用目标'}
            </button>
            <button className="admin-row-action" type="button" onClick={() => updateAllAssignments(target, true)}>全节点启用</button>
            <button className="admin-row-action" type="button" onClick={() => updateAllAssignments(target, false)}>全节点停用</button>
            <button className="admin-row-action is-danger" type="button" onClick={() => confirmDelete(target)}>删除目标</button>
          </div>
        </article>
      ))}
    </div>
  )
}

function AdminTargetCreateModal({ onCreate, onClose }: { onCreate: (input: AdminProbeTargetInput) => void; onClose: () => void }) {
  const [targetType, setTargetType] = useState<ProbeType>('tcping')

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const name = String(formData.get('new-target-name') ?? '').trim()
    const type = normalizeTargetFormType(String(formData.get('new-target-type') ?? 'tcping'))
    const address = String(formData.get('new-target-address') ?? '').trim()
    const port = type === 'tcping' ? parsePositiveInt(String(formData.get('new-target-port') ?? '')) : null
    if (name === '' || address === '' || (type === 'tcping' && port === null)) return
    onCreate({
      name,
      type,
      address,
      port,
      count: parsePositiveInt(String(formData.get('new-target-count') ?? '')) ?? 3,
      timeoutMs: parsePositiveInt(String(formData.get('new-target-timeout-ms') ?? '')) ?? 1200,
      intervalSec: parsePositiveInt(String(formData.get('new-target-interval-sec') ?? '')) ?? 60,
    })
  }

  return (
    <AdminModal title="添加延迟监控目标" eyebrow="Latency" onClose={onClose}>
      <form className="admin-target-create-form admin-node-edit-form" aria-label="添加探针目标" onSubmit={handleSubmit}>
        <label>
          <span>目标名称</span>
          <input name="new-target-name" autoComplete="off" placeholder="Example HTTPS" />
        </label>
        <label>
          <span>类型</span>
          <select name="new-target-type" value={targetType} onChange={(event) => setTargetType(normalizeTargetFormType(event.currentTarget.value))}>
            <option value="tcping">TCP Ping</option>
            <option value="ping">ICMP Ping</option>
            <option value="http_get">HTTP GET</option>
          </select>
        </label>
        <label>
          <span>地址</span>
          <input name="new-target-address" autoComplete="off" placeholder="example.com" />
        </label>
        {targetType === 'tcping' && (
          <label>
            <span>端口</span>
            <input name="new-target-port" type="number" min="1" max="65535" defaultValue="443" />
          </label>
        )}
        <label>
          <span>次数</span>
          <input name="new-target-count" type="number" min="1" defaultValue="3" />
        </label>
        <label>
          <span>超时 ms</span>
          <input name="new-target-timeout-ms" type="number" min="1" defaultValue="1200" />
        </label>
        <label>
          <span>间隔 s</span>
          <input name="new-target-interval-sec" type="number" min="1" defaultValue="60" />
        </label>
        <button type="submit">添加目标</button>
      </form>
    </AdminModal>
  )
}

function AdminTargetEditModal({ target, nodes, onUpdate, onClose }: { target: AdminProbeTarget; nodes: AdminNode[]; onUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => void; onClose: () => void }) {
  const [targetType, setTargetType] = useState<ProbeType>(target.type)
  const assignmentRows = targetAssignmentRows(target, nodes)

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const type = normalizeTargetFormType(String(formData.get('target-type') ?? targetType))
    const port = type === 'tcping' ? parsePositiveInt(String(formData.get('target-port') ?? '')) : null
    if (type === 'tcping' && port === null) return
    onUpdate(target.id, {
      name: String(formData.get('target-name') ?? ''),
      type,
      address: String(formData.get('target-address') ?? ''),
      port,
      count: parsePositiveInt(String(formData.get('target-count') ?? '')) ?? target.count,
      timeoutMs: parsePositiveInt(String(formData.get('target-timeout-ms') ?? '')) ?? target.timeoutMs,
      intervalSec: parsePositiveInt(String(formData.get('target-interval-sec') ?? '')) ?? target.intervalSec,
      enabled: formData.get('target-enabled') === 'on',
      assignments: assignmentRows.length > 0
        ? assignmentRows.map((assignment) => ({
            nodeId: assignment.nodeId,
            enabled: formData.get(`target-assignment-${assignment.nodeId}`) === 'on',
          }))
        : undefined,
    })
  }

  return (
    <AdminModal title={`编辑延迟监控 · ${target.name}`} eyebrow={target.id} onClose={onClose}>
      <dl className="admin-modal-summary">
        <div><dt>状态</dt><dd>{target.enabled ? 'enabled' : 'disabled'}</dd></div>
        <div><dt>类型</dt><dd>{formatTargetTypeLabel(target.type)}</dd></div>
        <div><dt>地址</dt><dd>{formatTargetEndpoint(target)}</dd></div>
        <div><dt>参数</dt><dd>{target.count} 次 / {target.timeoutMs}ms / {target.intervalSec}s</dd></div>
        <div><dt>节点</dt><dd>{formatTargetAssignmentSummary(target)}</dd></div>
      </dl>
      <form className="admin-target-edit-form admin-node-edit-form" aria-label={`${target.name} 探针目标编辑`} onSubmit={handleSubmit}>
        <label>
          <span>目标名</span>
          <input name="target-name" defaultValue={target.name} autoComplete="off" />
        </label>
        <label>
          <span>类型</span>
          <select name="target-type" value={targetType} onChange={(event) => setTargetType(normalizeTargetFormType(event.currentTarget.value))}>
            <option value="tcping">TCP Ping</option>
            <option value="ping">ICMP Ping</option>
            <option value="http_get">HTTP GET</option>
          </select>
        </label>
        <label>
          <span>地址</span>
          <input name="target-address" defaultValue={target.address} autoComplete="off" />
        </label>
        {targetType === 'tcping' && (
          <label>
            <span>端口</span>
            <input name="target-port" type="number" min="1" max="65535" defaultValue={target.port ?? ''} />
          </label>
        )}
        <label>
          <span>次数</span>
          <input name="target-count" type="number" min="1" defaultValue={target.count} />
        </label>
        <label>
          <span>超时 ms</span>
          <input name="target-timeout-ms" type="number" min="1" defaultValue={target.timeoutMs} />
        </label>
        <label>
          <span>间隔 s</span>
          <input name="target-interval-sec" type="number" min="1" defaultValue={target.intervalSec} />
        </label>
        <label className="admin-node-toggle">
          <input name="target-enabled" type="checkbox" defaultChecked={target.enabled} />
          <span>启用目标</span>
        </label>
        {assignmentRows.length > 0 && (
          <fieldset className="admin-target-assignment-list">
            <legend>按节点启用</legend>
            {assignmentRows.map((assignment) => (
              <label className="admin-node-toggle admin-target-assignment-toggle" key={assignment.nodeId}>
                <input name={`target-assignment-${assignment.nodeId}`} type="checkbox" defaultChecked={assignment.enabled} />
                <span>{assignment.nodeDisplayName || assignment.nodeId}</span>
              </label>
            ))}
          </fieldset>
        )}
        <div className="admin-modal-actions">
          <button type="submit">保存目标</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminNotificationsSection({ channels, types, deliveries, onChannelCreate, onChannelUpdate, onChannelDelete, onChannelTest, onTypeUpdate }: { channels: AdminNotificationChannel[]; types: AdminNotificationType[]; deliveries: AdminNotificationDelivery[]; onChannelCreate: (input: AdminNotificationChannelCreateInput) => void; onChannelUpdate: (channelId: string, input: AdminNotificationChannelUpdateInput) => void; onChannelDelete: (channelId: string) => void; onChannelTest: (channelId: string) => void; onTypeUpdate: (eventType: string, enabled: boolean) => void }) {
  const [creatingChannel, setCreatingChannel] = useState(false)
  const [editingChannel, setEditingChannel] = useState<AdminNotificationChannel | null>(null)

  return (
    <section className="admin-notification-section admin-workspace-panel" aria-label="admin notification settings">
      <header className="admin-section-heading">
        <div>
          <p className="eyebrow">Notify</p>
          <h3>通知</h3>
        </div>
        <button className="admin-primary-action" type="button" onClick={() => setCreatingChannel(true)}>添加通知渠道</button>
      </header>

      <section className="admin-notification-block" aria-label="通知渠道">
        <h4>通知渠道</h4>
        {channels.length === 0 && <div className="admin-state-card">还没有通知渠道。</div>}
        {channels.length > 0 && <AdminNotificationChannelList channels={channels} onUpdate={onChannelUpdate} onDelete={onChannelDelete} onTest={onChannelTest} onEdit={setEditingChannel} />}
      </section>

      <section className="admin-notification-block" aria-label="通知类型">
        <h4>通知类型</h4>
        <div className="admin-notification-type-grid">
          {types.map((notificationType) => (
            <article className="admin-action-card admin-notification-type-card" key={notificationType.eventType}>
              <p>{notificationType.label}</p>
              <strong>{notificationType.eventType}</strong>
              <span className={`admin-node-status status-${notificationType.enabled ? 'online' : 'disabled'}`}>{notificationType.enabled ? '启用中' : '已停用'}</span>
              <button className="admin-row-action" type="button" onClick={() => onTypeUpdate(notificationType.eventType, !notificationType.enabled)}>
                {notificationType.enabled ? '停用通知类型' : '启用通知类型'}
              </button>
            </article>
          ))}
        </div>
      </section>

      <section className="admin-notification-block" aria-label="最近发送">
        <h4>最近发送</h4>
        {deliveries.length === 0 && <div className="admin-state-card">还没有通知发送记录。</div>}
        {deliveries.length > 0 && <AdminNotificationDeliveryList deliveries={deliveries} />}
      </section>

      {creatingChannel && (
        <AdminNotificationChannelCreateModal
          onClose={() => setCreatingChannel(false)}
          onCreate={(input) => {
            onChannelCreate(input)
            setCreatingChannel(false)
          }}
        />
      )}
      {editingChannel && (
        <AdminNotificationChannelEditModal
          channel={editingChannel}
          onClose={() => setEditingChannel(null)}
          onUpdate={(channelId, input) => {
            onChannelUpdate(channelId, input)
            setEditingChannel(null)
          }}
        />
      )}
    </section>
  )
}

function AdminNotificationDeliveryList({ deliveries }: { deliveries: AdminNotificationDelivery[] }) {
  return (
    <div className="admin-list admin-notification-delivery-list" role="list" aria-label="通知发送记录">
      <div className="admin-list-head" aria-hidden="true">
        <span>事件</span>
        <span>节点</span>
        <span>渠道</span>
        <span>状态</span>
        <span>时间</span>
        <span>结果</span>
      </div>
      {deliveries.map((delivery) => (
        <article className="admin-list-row" role="listitem" key={delivery.id}>
          <div className="admin-list-main">
            <strong>{delivery.label}</strong>
            <small>{delivery.eventType}</small>
          </div>
          <span>{delivery.nodeName || delivery.nodeId}</span>
          <span>{delivery.channelName || delivery.channelId} · {formatNotificationChannelType(delivery.channelType)}</span>
          <span>{delivery.previousStatus} → {delivery.status}</span>
          <span>{formatAdminDate(delivery.createdAt)}</span>
          <span className={`admin-node-status status-${delivery.success ? 'online' : 'disabled'}`}>{delivery.success ? '发送成功' : '发送失败'}{delivery.error ? ` · ${delivery.error}` : ''}</span>
        </article>
      ))}
    </div>
  )
}

function AdminNotificationChannelList({ channels, onUpdate, onDelete, onTest, onEdit }: { channels: AdminNotificationChannel[]; onUpdate: (channelId: string, input: AdminNotificationChannelUpdateInput) => void; onDelete: (channelId: string) => void; onTest: (channelId: string) => void; onEdit: (channel: AdminNotificationChannel) => void }) {
  const confirmDelete = (channel: AdminNotificationChannel) => {
    const ok = typeof window === 'undefined' ? true : window.confirm(`确认删除通知渠道「${channel.name}」？`)
    if (ok) onDelete(channel.id)
  }

  return (
    <div className="admin-list admin-notification-list" role="list" aria-label="通知渠道列表">
      <div className="admin-list-head" aria-hidden="true">
        <span>渠道</span>
        <span>状态</span>
        <span>类型</span>
        <span>目标</span>
        <span>凭据</span>
        <span>操作</span>
      </div>
      {channels.map((channel) => (
        <article className="admin-list-row" role="listitem" key={channel.id}>
          <div className="admin-list-main">
            <strong>{channel.name}</strong>
            <small>{channel.id}</small>
          </div>
          <span className={`admin-node-status status-${channel.enabled ? 'online' : 'disabled'}`}>{channel.enabled ? '启用中' : '已停用'}</span>
          <span>{formatNotificationChannelType(channel.type)}</span>
          <span className="admin-notification-destination">{channel.destination}</span>
          <span>{channel.credentialSet ? '凭据已设置' : '未设置凭据'}</span>
          <div className="admin-row-actions">
            <button className="admin-row-action" type="button" onClick={() => onTest(channel.id)}>测试发送</button>
            <button className="admin-row-action" type="button" onClick={() => onEdit(channel)}>编辑渠道</button>
            <button className="admin-row-action" type="button" onClick={() => onUpdate(channel.id, { enabled: !channel.enabled })}>
              {channel.enabled ? '停用渠道' : '启用渠道'}
            </button>
            <button className="admin-row-action is-danger" type="button" onClick={() => confirmDelete(channel)}>删除渠道</button>
          </div>
        </article>
      ))}
    </div>
  )
}

function AdminNotificationChannelEditModal({ channel, onUpdate, onClose }: { channel: AdminNotificationChannel; onUpdate: (channelId: string, input: AdminNotificationChannelUpdateInput) => void; onClose: () => void }) {
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const name = String(formData.get('channel-name') ?? '').trim()
    const type = String(formData.get('channel-type') ?? channel.type) === 'telegram' ? 'telegram' : 'webhook'
    const destination = String(formData.get('channel-destination') ?? '').trim()
    const credential = String(formData.get('channel-credential') ?? '').trim()
    if (name === '' || destination === '') return
    onUpdate(channel.id, {
      name,
      type,
      destination,
      ...(credential !== '' ? { credential } : {}),
      enabled: formData.get('channel-enabled') === 'on',
    })
  }

  return (
    <AdminModal title="编辑通知渠道" eyebrow="Notify" onClose={onClose}>
      <form className="admin-notification-edit-form admin-node-edit-form" aria-label="编辑通知渠道" onSubmit={handleSubmit}>
        <label>
          <span>渠道名称</span>
          <input name="channel-name" autoComplete="off" defaultValue={channel.name} />
        </label>
        <label>
          <span>类型</span>
          <select name="channel-type" defaultValue={channel.type}>
            <option value="webhook">Webhook</option>
            <option value="telegram">Telegram</option>
          </select>
        </label>
        <label>
          <span>目标</span>
          <input name="channel-destination" autoComplete="off" defaultValue={channel.destination} />
        </label>
        <label>
          <span>凭据</span>
          <input name="channel-credential" type="password" autoComplete="new-password" placeholder={channel.credentialSet ? '留空则保留当前凭据' : '仅写入，不回显'} />
        </label>
        <label className="admin-node-toggle">
          <input name="channel-enabled" type="checkbox" defaultChecked={channel.enabled} />
          <span>启用渠道</span>
        </label>
        <div className="admin-modal-actions">
          <button type="submit">保存通知渠道</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminNotificationChannelCreateModal({ onCreate, onClose }: { onCreate: (input: AdminNotificationChannelCreateInput) => void; onClose: () => void }) {
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const name = String(formData.get('new-channel-name') ?? '').trim()
    const type = String(formData.get('new-channel-type') ?? 'webhook') === 'telegram' ? 'telegram' : 'webhook'
    const destination = String(formData.get('new-channel-destination') ?? '').trim()
    const credential = String(formData.get('new-channel-credential') ?? '').trim()
    if (name === '' || destination === '' || credential === '') return
    onCreate({
      name,
      type,
      destination,
      credential,
      enabled: formData.get('new-channel-enabled') === 'on',
    })
  }

  return (
    <AdminModal title="添加通知渠道" eyebrow="Notify" onClose={onClose}>
      <form className="admin-notification-create-form admin-node-edit-form" aria-label="添加通知渠道" onSubmit={handleSubmit}>
        <label>
          <span>渠道名称</span>
          <input name="new-channel-name" autoComplete="off" placeholder="Zeno Webhook" />
        </label>
        <label>
          <span>类型</span>
          <select name="new-channel-type" defaultValue="webhook">
            <option value="webhook">Webhook</option>
            <option value="telegram">Telegram</option>
          </select>
        </label>
        <label>
          <span>目标</span>
          <input name="new-channel-destination" autoComplete="off" placeholder="Webhook URL 或 Telegram chat ID" />
        </label>
        <label>
          <span>凭据</span>
          <input name="new-channel-credential" type="password" autoComplete="new-password" placeholder="仅写入，不回显" />
        </label>
        <label className="admin-node-toggle">
          <input name="new-channel-enabled" type="checkbox" defaultChecked />
          <span>创建后启用渠道</span>
        </label>
        <div className="admin-modal-actions">
          <button type="submit">保存通知渠道</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminModal({ title, eyebrow, onClose, children }: { title: string; eyebrow: string; onClose: () => void; children: ReactNode }) {
  return (
    <div className="admin-modal-backdrop" role="presentation">
      <section className="admin-modal" role="dialog" aria-modal="true" aria-label={title}>
        <header className="admin-modal-header">
          <div>
            <p className="eyebrow">{eyebrow}</p>
            <h3>{title}</h3>
          </div>
          <button className="admin-modal-close" type="button" onClick={onClose} aria-label="关闭弹窗">×</button>
        </header>
        {children}
      </section>
    </div>
  )
}

function sortAdminProbeTargets(targets: AdminProbeTarget[], sort: AdminTargetSort): AdminProbeTarget[] {
  return [...targets].sort((left, right) => {
    const byName = left.name.localeCompare(right.name, 'zh-CN') || left.id.localeCompare(right.id, 'zh-CN')
    if (sort === 'status') {
      return Number(right.enabled) - Number(left.enabled) || byName
    }
    if (sort === 'type') {
      return formatTargetTypeLabel(left.type).localeCompare(formatTargetTypeLabel(right.type), 'zh-CN') || byName
    }
    if (sort === 'assignments') {
      const rightEnabled = right.assignments.filter((assignment) => assignment.enabled).length
      const leftEnabled = left.assignments.filter((assignment) => assignment.enabled).length
      return rightEnabled - leftEnabled || byName
    }
    return byName
  })
}

function targetAssignmentRows(target: AdminProbeTarget, nodes: AdminNode[]) {
  const assignmentByNodeID = new Map(target.assignments.map((assignment) => [assignment.nodeId, assignment]))
  const nodeAssignmentRows = nodes.map((node) => {
    const assignment = assignmentByNodeID.get(node.id)
    return {
      nodeId: node.id,
      nodeDisplayName: node.displayName,
      enabled: assignment?.enabled ?? false,
    }
  })
  const staleAssignmentRows = target.assignments
    .filter((assignment) => !nodes.some((node) => node.id === assignment.nodeId))
    .map((assignment) => ({
      nodeId: assignment.nodeId,
      nodeDisplayName: assignment.nodeDisplayName || assignment.nodeId,
      enabled: assignment.enabled,
    }))
  return [...nodeAssignmentRows, ...staleAssignmentRows]
}

function formatTargetTypeLabel(type: ProbeType): string {
  if (type === 'ping') return 'ICMP Ping'
  if (type === 'http_get') return 'HTTP GET'
  return 'TCP Ping'
}

function normalizeTargetFormType(value: string): ProbeType {
  if (value === 'ping' || value === 'icmp') return 'ping'
  if (value === 'http_get' || value === 'http' || value === 'https') return 'http_get'
  return 'tcping'
}

function formatTargetEndpoint(target: AdminProbeTarget): string {
  return target.port ? `${target.address}:${target.port}` : target.address
}

function formatTargetAssignmentSummary(target: AdminProbeTarget): string {
  if (target.assignments.length === 0) return '未分配节点'
  const enabled = target.assignments.filter((assignment) => assignment.enabled).length
  return `${enabled} / ${target.assignments.length} 节点启用`
}

function formatNotificationChannelType(type: AdminNotificationChannel['type']): string {
  return type === 'telegram' ? 'Telegram' : 'Webhook'
}

function parsePositiveInt(value: string): number | null {
  const parsed = Number(value.trim())
  if (!Number.isInteger(parsed) || parsed <= 0) return null
  return parsed
}

function formatQuotaGigabytes(value: number | null): string {
  if (!value || value <= 0) return ''
  const gigabytes = value / (1024 ** 3)
  return String(Math.round(gigabytes * 100) / 100)
}

function parseQuotaGigabytes(value: string): number | null {
  const trimmed = value.trim()
  if (trimmed === '') return null
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed) || parsed < 0) return null
  return Math.round(parsed * (1024 ** 3))
}

function formatAdminSystem(node: AdminNode): string {
  const system = [node.osName, node.osVersion].filter(Boolean).join(' ')
  return system || node.arch || '—'
}

function formatAdminResources(node: AdminNode): string {
  const cpu = node.cpuCores ? `${node.cpuCores}C` : '—'
  const mem = node.memoryTotalBytes ? compactBytes(node.memoryTotalBytes) : '—'
  const disk = node.diskTotalBytes ? compactBytes(node.diskTotalBytes) : '—'
  return `${cpu} / ${mem} / ${disk}`
}

function formatAdminDate(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

export function HomeOverviewPanel({ totalCount, onlineCount, offlineCount, totalUp, totalDown, upSpeed, downSpeed }: HomeOverviewPanelProps) {
  const onlineRatio = totalCount > 0 ? Math.round((onlineCount / totalCount) * 100) : 0
  const nonOnlineCount = Math.max(totalCount - onlineCount, offlineCount, 0)
  const healthLabel = totalCount === 0 ? '等待接入' : nonOnlineCount === 0 ? '全部在线' : `${nonOnlineCount} 台未在线`
  const healthTone = totalCount > 0 && nonOnlineCount === 0 ? 'is-good' : totalCount > 0 ? 'is-warning' : ''

  return (
    <section className="home-summary" aria-label="server overview">
      <div className="home-summary__intro">
        <div>
          <p className="eyebrow">Zeno Overview</p>
          <h1>服务器运行概览</h1>
        </div>
        <span className={`home-health-pill ${healthTone}`}>{healthLabel}</span>
      </div>

      <div className="home-summary__compact">
        <div className="home-health-block">
          <div className="home-health-number">
            <strong>{onlineCount}</strong>
            <span>/ {totalCount} 在线</span>
          </div>
          <div className="home-health-bar" aria-label={`在线率 ${onlineRatio}%`}>
            <span style={{ transform: `scaleX(${onlineRatio / 100})` }} />
          </div>
          <p>{healthLabel} · 在线率 {onlineRatio}%</p>
        </div>

        <dl className="home-network-grid" aria-label="traffic totals and speeds">
          <div>
            <dt>累计上传</dt>
            <dd>{compactBytes(totalUp)}</dd>
          </div>
          <div>
            <dt>累计下载</dt>
            <dd>{compactBytes(totalDown)}</dd>
          </div>
          <div>
            <dt>实时上传</dt>
            <dd><CircleArrowIcon direction="up" />{compactRate(upSpeed)}</dd>
          </div>
          <div>
            <dt>实时下载</dt>
            <dd><CircleArrowIcon direction="down" />{compactRate(downSpeed)}</dd>
          </div>
        </dl>
      </div>
    </section>
  )
}

function MapIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M14.106 5.553a2 2 0 0 0 1.788 0l3.659-1.83A1 1 0 0 1 21 4.619v12.764a1 1 0 0 1-.553.894l-4.553 2.277a2 2 0 0 1-1.788 0l-4.212-2.106a2 2 0 0 0-1.788 0l-3.659 1.83A1 1 0 0 1 3 19.381V6.618a1 1 0 0 1 .553-.894l4.553-2.277a2 2 0 0 1 1.788 0z" />
      <path d="M15 5.764v15" />
      <path d="M9 3.236v15" />
    </svg>
  )
}

function SunIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41" />
    </svg>
  )
}

function ImageMinusIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M21 9v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h7" />
      <path d="M16 5h6" />
      <circle cx="9" cy="9" r="2" />
      <path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21" />
    </svg>
  )
}

function CircleArrowIcon({ direction }: { direction: 'up' | 'down' }) {
  return direction === 'up' ? (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path fillRule="evenodd" d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Zm-.75-4.75a.75.75 0 0 0 1.5 0V8.66l1.95 2.1a.75.75 0 1 0 1.1-1.02l-3.25-3.5a.75.75 0 0 0-1.1 0L6.2 9.74a.75.75 0 1 0 1.1 1.02l1.95-2.1v4.59Z" clipRule="evenodd" />
    </svg>
  ) : (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path fillRule="evenodd" d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Zm.75-11.25a.75.75 0 0 0-1.5 0v4.59L7.3 9.24a.75.75 0 0 0-1.1 1.02l3.25 3.5a.75.75 0 0 0 1.1 0l3.25-3.5a.75.75 0 1 0-1.1-1.02l-1.95 2.1V6.75Z" clipRule="evenodd" />
    </svg>
  )
}
