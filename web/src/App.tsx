import { type FormEvent, useEffect, useState } from 'react'
import { createAdminNode, createAdminProbeTarget, fetchAdminNodes, fetchAdminProbeTargets, fetchNodeLatency, fetchNodeState, fetchSummary, requestAdminNodeInstallCommand, updateAdminNode, updateAdminProbeTarget, type AdminNodeCreateInput, type AdminNodeUpdateInput, type AdminProbeTargetInput, type AdminProbeTargetUpdateInput, type NodeLatencyData, type NodeStateData, type SummaryData } from './api/client'
import { LatencyDetail } from './components/LatencyDetail'
import { ServerCard } from './components/ServerCard'
import { startLiveRefresh } from './lib/liveRefresh'
import { nodePath, parseDashboardRoute, type DashboardRoute } from './lib/route'
import type { AdminNode, AdminProbeTarget } from './types'

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
  | { kind: 'ready'; nodes: AdminNode[]; targets: AdminProbeTarget[] }
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
      Promise.all([fetchAdminNodes(adminToken), fetchAdminProbeTargets(adminToken)])
        .then(([nodesData, targetsData]) => {
          loadedOnce = true
          if (!cancelled) setAdminState({ kind: 'ready', nodes: nodesData.nodes, targets: targetsData.targets })
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
    Promise.all([fetchAdminNodes(adminToken), fetchAdminProbeTargets(adminToken)])
      .then(([nodesData, targetsData]) => setAdminState({ kind: 'ready', nodes: nodesData.nodes, targets: targetsData.targets }))
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const createAdminNodeDetails = (input: AdminNodeCreateInput) => {
    if (adminToken === '') return
    createAdminNode(adminToken, input)
      .then((createdNode) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { kind: 'ready', nodes: [...current.nodes, createdNode], targets: current.targets }
          }
          return { kind: 'ready', nodes: [createdNode], targets: [] }
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
            return { kind: 'ready', nodes: current.nodes.map((node) => node.id === updatedNode.id ? updatedNode : node), targets: current.targets }
          }
          return { kind: 'ready', nodes: [updatedNode], targets: [] }
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
            return { kind: 'ready', nodes: current.nodes, targets: [...current.targets, createdTarget] }
          }
          return { kind: 'ready', nodes: [], targets: [createdTarget] }
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
            return { kind: 'ready', nodes: current.nodes, targets: current.targets.map((target) => target.id === updatedTarget.id ? updatedTarget : target) }
          }
          return { kind: 'ready', nodes: [], targets: [updatedTarget] }
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
  onAdminNodeCreate?: (input: AdminNodeCreateInput) => void
  onAdminNodeUpdate?: (nodeId: string, input: AdminNodeUpdateInput) => void
  onAdminInstallCommand?: (nodeId: string) => Promise<string>
  onAdminProbeTargetCreate?: (input: AdminProbeTargetInput) => void
  onAdminProbeTargetUpdate?: (targetId: string, input: AdminProbeTargetUpdateInput) => void
}

export function AdminDashboard({
  onHome,
  hasAdminToken = false,
  adminState = { kind: 'idle' },
  onAdminTokenSubmit = () => {},
  onAdminTokenClear = () => {},
  onAdminRefresh = () => {},
  onAdminNodeCreate = () => {},
  onAdminNodeUpdate = () => {},
  onAdminInstallCommand = () => Promise.reject(new Error('install command unavailable')),
  onAdminProbeTargetCreate = () => {},
  onAdminProbeTargetUpdate = () => {},
}: AdminDashboardProps) {
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
            <strong>{hasAdminToken ? `${targetCount} 个目标` : 'Agent 与目标'}</strong>
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

        {!hasAdminToken && (
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
        )}

        {hasAdminToken && (
          <>
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

              <AdminNodeCreateForm onCreate={onAdminNodeCreate} />

              {adminState.kind === 'loading' && <div className="admin-state-card">正在读取 Admin API…</div>}
              {adminState.kind === 'error' && <div className="admin-state-card is-error">Admin API 读取失败：{adminState.message}</div>}
              {adminState.kind === 'ready' && adminState.nodes.length === 0 && <div className="admin-state-card">还没有节点。</div>}
              {adminState.kind === 'ready' && adminState.nodes.length > 0 && (
                <div className="admin-node-grid">
                  {adminState.nodes.map((node) => <AdminNodeCard key={node.id} node={node} onUpdate={onAdminNodeUpdate} onInstallCommand={onAdminInstallCommand} />)}
                </div>
              )}
            </section>

            {adminState.kind === 'ready' && (
              <section className="admin-target-section" aria-label="admin probe target list">
                <header className="admin-section-heading">
                  <div>
                    <p className="eyebrow">Targets</p>
                    <h3>探针目标</h3>
                  </div>
                </header>
                <AdminTargetCreateForm onCreate={onAdminProbeTargetCreate} />
                {adminState.targets.length === 0 && <div className="admin-state-card">还没有探针目标。</div>}
                {adminState.targets.length > 0 && (
                  <div className="admin-target-grid">
                    {adminState.targets.map((target) => <AdminTargetCard key={target.id} target={target} nodes={adminState.nodes} onUpdate={onAdminProbeTargetUpdate} />)}
                  </div>
                )}
              </section>
            )}
          </>
        )}
      </section>
    </div>
  )
}

function AdminNodeCreateForm({ onCreate }: { onCreate: (input: AdminNodeCreateInput) => void }) {
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
    form.reset()
  }

  return (
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
  )
}

function AdminNodeCard({ node, onUpdate, onInstallCommand }: { node: AdminNode; onUpdate: (nodeId: string, input: AdminNodeUpdateInput) => void; onInstallCommand: (nodeId: string) => Promise<string> }) {
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
        <button type="button" onClick={handleInstallCommand} disabled={installCommandState.kind === 'loading'}>{installCommandState.kind === 'loading' ? '生成中…' : '获取安装命令'}</button>
      </form>
      {installCommandState.kind === 'ready' && (
        <textarea className="admin-install-command" aria-label={`${node.displayName} Agent 安装命令`} readOnly value={installCommandState.command} />
      )}
      {installCommandState.kind === 'error' && <div className="admin-install-error">安装命令生成失败：{installCommandState.message}</div>}
    </article>
  )
}

function AdminTargetCreateForm({ onCreate }: { onCreate: (input: AdminProbeTargetInput) => void }) {
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const formData = new FormData(form)
    const name = String(formData.get('new-target-name') ?? '').trim()
    const address = String(formData.get('new-target-address') ?? '').trim()
    const port = parsePositiveInt(String(formData.get('new-target-port') ?? ''))
    if (name === '' || address === '' || port === null) return
    onCreate({
      name,
      type: 'tcping',
      address,
      port,
      count: parsePositiveInt(String(formData.get('new-target-count') ?? '')) ?? 3,
      timeoutMs: parsePositiveInt(String(formData.get('new-target-timeout-ms') ?? '')) ?? 1200,
      intervalSec: parsePositiveInt(String(formData.get('new-target-interval-sec') ?? '')) ?? 60,
    })
    form.reset()
  }

  return (
    <form className="admin-target-create-form admin-node-edit-form" aria-label="添加探针目标" onSubmit={handleSubmit}>
      <label>
        <span>目标名称</span>
        <input name="new-target-name" autoComplete="off" placeholder="Example HTTPS" />
      </label>
      <label>
        <span>地址</span>
        <input name="new-target-address" autoComplete="off" placeholder="example.com" />
      </label>
      <label>
        <span>端口</span>
        <input name="new-target-port" type="number" min="1" max="65535" defaultValue="443" />
      </label>
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
  )
}

function AdminTargetCard({ target, nodes, onUpdate }: { target: AdminProbeTarget; nodes: AdminNode[]; onUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => void }) {
  const endpoint = target.port ? `${target.address}:${target.port}` : target.address
  const assignments = target.assignments.length > 0
    ? target.assignments.map((assignment) => `${assignment.nodeDisplayName || assignment.nodeId}${assignment.enabled ? '' : '（停用）'}`).join('、')
    : '未分配节点'
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
  const assignmentRows = [...nodeAssignmentRows, ...staleAssignmentRows]

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const port = parsePositiveInt(String(formData.get('target-port') ?? ''))
    if (port === null) return
    onUpdate(target.id, {
      name: String(formData.get('target-name') ?? ''),
      type: 'tcping',
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
    <article className="admin-target-card">
      <header>
        <div>
          <p>{target.id}</p>
          <h4>{target.name}</h4>
        </div>
        <span className={`admin-node-status status-${target.enabled ? 'online' : 'disabled'}`}>{target.enabled ? 'enabled' : 'disabled'}</span>
      </header>
      <dl className="admin-node-meta">
        <div><dt>类型</dt><dd>{target.type}</dd></div>
        <div><dt>地址</dt><dd>{endpoint}</dd></div>
        <div><dt>参数</dt><dd>{target.count} 次 / {target.timeoutMs}ms / {target.intervalSec}s</dd></div>
        <div><dt>节点</dt><dd>{assignments}</dd></div>
      </dl>
      <form className="admin-target-edit-form admin-node-edit-form" aria-label={`${target.name} 探针目标编辑`} onSubmit={handleSubmit}>
        <label>
          <span>目标名</span>
          <input name="target-name" defaultValue={target.name} autoComplete="off" />
        </label>
        <label>
          <span>地址</span>
          <input name="target-address" defaultValue={target.address} autoComplete="off" />
        </label>
        <label>
          <span>端口</span>
          <input name="target-port" type="number" min="1" max="65535" defaultValue={target.port ?? ''} />
        </label>
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
        <button type="submit">保存目标</button>
      </form>
    </article>
  )
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
