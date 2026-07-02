import { useEffect, useState } from 'react'
import { fetchSummary, type SummaryData } from './api/client'
import { LatencyChart } from './components/LatencyChart'
import { ServerCard } from './components/ServerCard'

type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; data: SummaryData }
  | { kind: 'error'; message: string }

export function App() {
  const [state, setState] = useState<LoadState>({ kind: 'loading' })

  useEffect(() => {
    let cancelled = false
    fetchSummary()
      .then((data) => {
        if (!cancelled) setState({ kind: 'ready', data })
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
        }
      })
    return () => { cancelled = true }
  }, [])

  const nodes = state.kind === 'ready' ? state.data.nodes : []
  const latencyPoints = state.kind === 'ready' ? state.data.latencyPoints : []
  const onlineCount = nodes.filter((node) => node.status === 'online').length

  return (
    <main className="app-shell">
      <section className="hero">
        <div>
          <p className="eyebrow">JiaoProbe / 饺探</p>
          <h1>水饺服务器状态</h1>
          <p className="hero__subcopy">Public API mock 阶段：Controller 提供 mock JSON，前端从 `/api/public/v1/summary` 读取，不再直接 import mock 数据。</p>
        </div>
        <div className="hero__status">
          <strong>{state.kind === 'ready' ? `${onlineCount}/${nodes.length}` : '--'}</strong>
          <span>online</span>
        </div>
      </section>

      {state.kind === 'loading' && <section className="state-panel">正在读取 Controller API…</section>}
      {state.kind === 'error' && <section className="state-panel is-error">API 读取失败：{state.message}</section>}

      {state.kind === 'ready' && (
        <>
          <section className="cards-grid" aria-label="server cards">
            {nodes.map((node) => <ServerCard key={node.id} node={node} />)}
          </section>

          <LatencyChart points={latencyPoints} />
        </>
      )}
    </main>
  )
}
