import { type FormEvent, useEffect, useState } from 'react'
import { fetchAdminNodes, fetchNodeLatency, fetchNodeState, fetchSummary, updateAdminNode, type AdminNodeUpdateInput, type NodeLatencyData, type NodeStateData, type SummaryData } from './api/client'
import { LatencyDetail } from './components/LatencyDetail'
import { ServerCard } from './components/ServerCard'
import { startLiveRefresh } from './lib/liveRefresh'
import { nodePath, parseDashboardRoute, type DashboardRoute } from './lib/route'
import type { AdminNode } from './types'

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
  | { kind: 'ready'; nodes: AdminNode[] }
  | { kind: 'error'; message: string }

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
  const [adminToken, setAdminToken] = useState(() => window.sessionStorage.getItem('jiaoprobe_admin_token') ?? '')
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
      fetchAdminNodes(adminToken)
        .then((data) => {
          loadedOnce = true
          if (!cancelled) setAdminState({ kind: 'ready', nodes: data.nodes })
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
    window.sessionStorage.setItem('jiaoprobe_admin_token', trimmed)
    setAdminToken(trimmed)
  }

  const clearAdminToken = () => {
    window.sessionStorage.removeItem('jiaoprobe_admin_token')
    setAdminToken('')
    setAdminState({ kind: 'idle' })
  }

  const refreshAdminNodes = () => {
    if (adminToken === '') return
    setAdminState({ kind: 'loading' })
    fetchAdminNodes(adminToken)
      .then((data) => setAdminState({ kind: 'ready', nodes: data.nodes }))
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const updateAdminNodeDetails = (nodeId: string, input: AdminNodeUpdateInput) => {
    if (adminToken === '') return
    updateAdminNode(adminToken, nodeId, input)
      .then((updatedNode) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { kind: 'ready', nodes: current.nodes.map((node) => node.id === updatedNode.id ? updatedNode : node) }
          }
          return { kind: 'ready', nodes: [updatedNode] }
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
          onAdminNodeUpdate={updateAdminNodeDetails}
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
        <span>水饺的探针</span>
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
  onAdminTokenSubmit?: (token: string) => void
  onAdminTokenClear?: () => void
  onAdminRefresh?: () => void
  onAdminNodeUpdate?: (nodeId: string, input: AdminNodeUpdateInput) => void
}

export function AdminDashboard({
  onHome,
  hasAdminToken = false,
  adminState = { kind: 'idle' },
  onAdminTokenSubmit = () => {},
  onAdminTokenClear = () => {},
  onAdminRefresh = () => {},
  onAdminNodeUpdate = () => {},
}: AdminDashboardProps) {
  const handleTokenSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const token = String(new FormData(form).get('admin-token') ?? '')
    onAdminTokenSubmit(token)
    form.reset()
  }

  const nodeCount = adminState.kind === 'ready' ? adminState.nodes.length : 0

  return (
    <div className="kulin-container admin-container">
      <section className="home-top-card admin-panel" aria-label="admin dashboard">
        <DashboardHeader onHome={onHome} onAdmin={onHome} adminLabel="前台" />
        <div className="admin-hero">
          <p className="eyebrow">JiaoProbe 后台</p>
          <h2>控制台</h2>
          <p>沿用前台卡片风格，节点管理已接入真实 Admin API；敏感凭据只通过请求头提交，不会展示在页面里。</p>
        </div>
        <div className="admin-action-grid" aria-label="admin modules">
          <article className="admin-action-card">
            <p>节点管理</p>
            <strong>{hasAdminToken ? `${nodeCount} 台节点` : '等待认证'}</strong>
          </article>
          <article className="admin-action-card">
            <p>探针配置</p>
            <strong>Agent 与目标</strong>
          </article>
          <article className="admin-action-card">
            <p>告警策略</p>
            <strong>规则与通知</strong>
          </article>
        </div>

        {!hasAdminToken && (
          <form className="admin-login-card" aria-label="admin token form" onSubmit={handleTokenSubmit}>
            <div>
              <p>后台认证</p>
              <strong>输入 Admin Token 后读取节点管理数据</strong>
            </div>
            <input name="admin-token" type="password" autoComplete="current-password" placeholder="Admin Token" aria-label="Admin Token" />
            <button type="submit">进入后台</button>
          </form>
        )}

        {hasAdminToken && (
          <section className="admin-node-section" aria-label="admin node list">
            <header className="admin-section-heading">
              <div>
                <p className="eyebrow">Nodes</p>
                <h3>节点列表</h3>
              </div>
              <div className="admin-section-actions">
                <button type="button" onClick={onAdminRefresh}>刷新</button>
                <button type="button" onClick={onAdminTokenClear}>退出</button>
              </div>
            </header>

            {adminState.kind === 'loading' && <div className="admin-state-card">正在读取 Admin API…</div>}
            {adminState.kind === 'error' && <div className="admin-state-card is-error">Admin API 读取失败：{adminState.message}</div>}
            {adminState.kind === 'ready' && adminState.nodes.length === 0 && <div className="admin-state-card">还没有节点。</div>}
            {adminState.kind === 'ready' && adminState.nodes.length > 0 && (
              <div className="admin-node-grid">
                {adminState.nodes.map((node) => <AdminNodeCard key={node.id} node={node} onUpdate={onAdminNodeUpdate} />)}
              </div>
            )}
          </section>
        )}
      </section>
    </div>
  )
}

function AdminNodeCard({ node, onUpdate }: { node: AdminNode; onUpdate: (nodeId: string, input: AdminNodeUpdateInput) => void }) {
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

  return (
    <article className="admin-node-card">
      <header>
        <div>
          <p>{node.id}</p>
          <h4>{node.displayName}</h4>
        </div>
        <span className={`admin-node-status status-${node.status}`}>{node.status}</span>
      </header>
      <dl className="admin-node-meta">
        <div><dt>系统</dt><dd>{formatAdminSystem(node)}</dd></div>
        <div><dt>Agent</dt><dd>{node.agentVersion || '—'}</dd></div>
        <div><dt>最近在线</dt><dd>{formatAdminDate(node.lastSeenAt)}</dd></div>
        <div><dt>流量模式</dt><dd>{node.billingMode || 'both'}</dd></div>
        <div><dt>月配额</dt><dd>{node.monthlyQuotaBytes ? compactBytes(node.monthlyQuotaBytes) : '—'}</dd></div>
        <div><dt>资源</dt><dd>{formatAdminResources(node)}</dd></div>
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
        <button type="submit">保存节点</button>
      </form>
    </article>
  )
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
  return (
    <section className="server-overview" aria-label="server overview">
      <div className="overview-combined__body">
        <OverviewMetric tone="blue" label="服务器总数" value={String(totalCount)} />
        <OverviewMetric tone="green" label="在线服务器" value={String(onlineCount)} pulse />
        <OverviewMetric tone="red" label="离线服务器" value={String(offlineCount)} pulse />
        <div className="overview-metric tone-purple">
          <p>网络</p>
          <section className="network-total" aria-label="traffic totals">
            <strong className="up">↑{compactBytes(totalUp)}</strong>
            <strong className="down">↓{compactBytes(totalDown)}</strong>
          </section>
          <section className="network-speed" aria-label="traffic speeds">
            <span><CircleArrowIcon direction="up" />{compactRate(upSpeed)}</span>
            <span><CircleArrowIcon direction="down" />{compactRate(downSpeed)}</span>
          </section>
        </div>
      </div>
    </section>
  )
}

function OverviewMetric({ label, value, tone, pulse = false }: { label: string; value: string; tone: 'blue' | 'green' | 'red'; pulse?: boolean }) {
  return (
    <div className={`overview-metric tone-${tone}`}>
      <p>{label}</p>
      <div className="overview-value">
        <span className="pulse-dot"><i className={pulse ? 'is-pulsing' : ''} /><b /></span>
        <strong aria-label={value}>{value}</strong>
      </div>
    </div>
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
