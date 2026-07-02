import { useEffect, useState } from 'react'
import { fetchSummary, type SummaryData } from './api/client'
import { ServerCard } from './components/ServerCard'

type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; data: SummaryData }
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

  useEffect(() => {
    let cancelled = false
    fetchSummary()
      .then((data) => {
        if (!cancelled) setState({ kind: 'ready', data })
      })
      .catch((error: unknown) => {
        if (!cancelled) setState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
      })
    return () => { cancelled = true }
  }, [])

  const nodes = state.kind === 'ready' ? state.data.nodes : []
  const totalCount = nodes.length
  const onlineCount = nodes.filter((node) => node.status === 'online').length
  const offlineCount = nodes.filter((node) => node.status === 'offline').length
  const totalUp = sum(nodes.map((node) => node.netOutTotalBytes))
  const totalDown = sum(nodes.map((node) => node.netInTotalBytes))
  const upSpeed = sum(nodes.map((node) => node.netOutSpeedBps))
  const downSpeed = sum(nodes.map((node) => node.netInSpeedBps))

  return (
    <main className="kulin-shell">
      <header className="kulin-nav">
        <div className="brand">
          <img src="/assets/logo/os-debian.svg" alt="apple-touch-icon" />
          <span>水饺的探针</span>
        </div>
        <nav className="nav-actions" aria-label="dashboard actions">
          <a href="https://shuijiao.li/" target="_blank" rel="noreferrer">登录</a>
          <button type="button" aria-label="language">中</button>
          <button type="button">切换主题</button>
          <button type="button" aria-label="menu">☰</button>
        </nav>
      </header>

      {state.kind === 'loading' && <section className="state-panel">正在读取 Controller API…</section>}
      {state.kind === 'error' && <section className="state-panel is-error">API 读取失败：{state.message}</section>}

      {state.kind === 'ready' && (
        <div className="kulin-container">
          <section className="server-overview" aria-label="server overview">
            <OverviewCard tone="blue" label="服务器总数" value={String(totalCount)} />
            <OverviewCard tone="green" label="在线服务器" value={String(onlineCount)} pulse />
            <OverviewCard tone="red" label="离线服务器" value={String(offlineCount)} pulse />
            <article className="overview-card tone-purple">
              <div className="overview-card__body network-overview">
                <p>网络</p>
                <div className="network-total">
                  <strong className="up">↑{compactBytes(totalUp)}</strong>
                  <strong className="down">↓{compactBytes(totalDown)}</strong>
                </div>
                <div className="network-speed">
                  <span>⬆ {compactRate(upSpeed)}</span>
                  <span>⬇ {compactRate(downSpeed)}</span>
                </div>
              </div>
            </article>
          </section>

          <section className="server-controls" aria-hidden="true" />

          <section className="server-card-list" aria-label="server cards">
            {nodes.map((node) => <ServerCard key={node.id} node={node} />)}
          </section>
        </div>
      )}
    </main>
  )
}

function OverviewCard({ label, value, tone, pulse = false }: { label: string; value: string; tone: 'blue' | 'green' | 'red'; pulse?: boolean }) {
  return (
    <article className={`overview-card tone-${tone}`}>
      <div className="overview-card__body">
        <p>{label}</p>
        <div className="overview-value">
          <span className="pulse-dot"><i className={pulse ? 'is-pulsing' : ''} /><b /></span>
          <strong>{value}</strong>
        </div>
      </div>
    </article>
  )
}
