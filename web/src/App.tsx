import { useEffect, useState } from 'react'
import { fetchNodeLatency, fetchSummary, type NodeLatencyData, type SummaryData } from './api/client'
import { LatencyChart } from './components/LatencyChart'
import { ServerCard } from './components/ServerCard'

// Public API data load for the dashboard shell.
type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; data: SummaryData }
  | { kind: 'error'; message: string }

type NodeLatencyState =
  | { kind: 'idle' }
  | { kind: 'loading'; nodeId: string }
  | { kind: 'ready'; data: NodeLatencyData }
  | { kind: 'error'; nodeId: string; message: string }

export function App() {
  const [state, setState] = useState<LoadState>({ kind: 'loading' })
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null)
  const [nodeLatency, setNodeLatency] = useState<NodeLatencyState>({ kind: 'idle' })

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
  const selectedNode = nodes.find((node) => node.id === selectedNodeId) ?? nodes[0]
  const effectiveSelectedNodeId = selectedNode?.id ?? null
  const onlineCount = nodes.filter((node) => node.status === 'online').length

  useEffect(() => {
    if (state.kind !== 'ready' || !effectiveSelectedNodeId) return

    let cancelled = false
    setNodeLatency({ kind: 'loading', nodeId: effectiveSelectedNodeId })
    fetchNodeLatency(effectiveSelectedNodeId, '1h')
      .then((data) => {
        if (!cancelled) setNodeLatency({ kind: 'ready', data })
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setNodeLatency({
            kind: 'error',
            nodeId: effectiveSelectedNodeId,
            message: error instanceof Error ? error.message : 'unknown error',
          })
        }
      })
    return () => { cancelled = true }
  }, [state.kind, effectiveSelectedNodeId])

  const selectedLatencyPoints = nodeLatency.kind === 'ready' && nodeLatency.data.nodeId === effectiveSelectedNodeId
    ? nodeLatency.data.points
    : []

  return (
    <main className="app-shell">
      <section className="hero">
        <div>
          <p className="eyebrow">JiaoProbe / 饺探</p>
          <h1>水饺服务器状态</h1>
          <p className="hero__subcopy">Public API mock 部署预览：Go Controller 同时提供 API 和前端静态页面；点击卡片详情会读取 `/api/public/v1/nodes/:id/latency`。</p>
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
            {nodes.map((node) => (
              <ServerCard
                key={node.id}
                node={node}
                isSelected={node.id === effectiveSelectedNodeId}
                onSelect={setSelectedNodeId}
              />
            ))}
          </section>

          {selectedNode && (
            <section className="node-detail-panel">
              <div>
                <p className="eyebrow">Selected node</p>
                <h2>{selectedNode.displayName} 详情预览</h2>
                <p>{selectedNode.subtitle ?? 'No subtitle'} · {selectedNode.status.replace('_', ' ')}</p>
              </div>
              <div className="node-detail-panel__metrics">
                <span><strong>{selectedNode.latencySummary?.targetName ?? '--'}</strong><em>主探测目标</em></span>
                <span><strong>{selectedNode.latencySummary?.medianMs ?? '--'}ms</strong><em>当前 median</em></span>
                <span><strong>{selectedNode.latencySummary?.lossPercent ?? 0}%</strong><em>packet loss</em></span>
              </div>
            </section>
          )}

          {nodeLatency.kind === 'loading' && <section className="state-panel">正在读取 {selectedNode?.displayName ?? effectiveSelectedNodeId} 节点延迟…</section>}
          {nodeLatency.kind === 'error' && <section className="state-panel is-error">节点延迟读取失败：{nodeLatency.message}</section>}
          {selectedLatencyPoints.length > 0 && (
            <LatencyChart eyebrow="Node latency" title={`${selectedNode?.displayName ?? '节点'} · 1h 多目标延迟`} points={selectedLatencyPoints} />
          )}

          <LatencyChart eyebrow="Overview latency" title="全局多目标延迟图" points={latencyPoints} />
        </>
      )}
    </main>
  )
}
