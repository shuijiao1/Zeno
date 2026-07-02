import type { ReactNode } from 'react'
import type { StatePoint } from '../types'
import { formatBps, formatPercent } from '../lib/format'

interface StateHistoryPanelProps {
  points: StatePoint[]
  rangeLabel: string
  loading?: boolean
  error?: string
}

interface MetricConfig {
  key: string
  label: string
  value: ReactNode
  tone: 'green' | 'blue' | 'purple' | 'orange'
  values: Array<number | null>
  unitLabel: string
}

const chartWidth = 300
const chartHeight = 92
const chartPad = { left: 8, right: 8, top: 10, bottom: 12 }

export function StateHistoryPanel({ points, rangeLabel, loading = false, error }: StateHistoryPanelProps) {
  const sampleCount = points.length
  const latestCpu = latest(points, (point) => point.cpuPercent)
  const latestMemory = latest(points, memoryPercent)
  const latestDisk = latest(points, diskPercent)
  const latestInSpeed = latest(points, (point) => point.netInSpeedBps)
  const latestOutSpeed = latest(points, (point) => point.netOutSpeedBps)

  const metrics: MetricConfig[] = [
    {
      key: 'cpu',
      label: 'CPU',
      value: formatPercent(latestCpu),
      tone: 'green',
      values: points.map((point) => finiteOrNull(point.cpuPercent)),
      unitLabel: '%',
    },
    {
      key: 'memory',
      label: '内存',
      value: formatPercent(latestMemory),
      tone: 'blue',
      values: points.map(memoryPercent),
      unitLabel: '%',
    },
    {
      key: 'disk',
      label: '磁盘',
      value: formatPercent(latestDisk),
      tone: 'purple',
      values: points.map(diskPercent),
      unitLabel: '%',
    },
    {
      key: 'network',
      label: '网络速率',
      value: <><span>↑{formatBps(latestOutSpeed)}</span><span>↓{formatBps(latestInSpeed)}</span></>,
      tone: 'orange',
      values: points.map((point) => sumFinite(point.netInSpeedBps, point.netOutSpeedBps)),
      unitLabel: 'B/s',
    },
  ]

  return (
    <section className="monitor-panel state-history-panel" aria-label="agent state history">
      <header className="monitor-heading">
        <div>
          <h3>系统资源历史</h3>
          <p>{rangeLabel} · {sampleCount} 个状态采样</p>
        </div>
      </header>

      {loading && <div className="detail-state">正在读取系统资源…</div>}
      {error && <div className="detail-state is-error">系统资源读取失败：{error}</div>}
      {!loading && !error && sampleCount === 0 && <div className="detail-state">暂无系统资源历史</div>}

      {!loading && !error && sampleCount > 0 && (
        <div className="state-history-grid">
          {metrics.map((metric) => <MetricCard key={metric.key} metric={metric} />)}
        </div>
      )}
    </section>
  )
}

function MetricCard({ metric }: { metric: MetricConfig }) {
  const domain = yDomain(metric.values)
  const path = sparklinePath(metric.values, domain)

  return (
    <article className={`state-history-card tone-${metric.tone}`}>
      <div className="state-history-card__meta">
        <p>{metric.label}</p>
        <strong>{metric.value}</strong>
      </div>
      <svg className="state-sparkline" viewBox={`0 0 ${chartWidth} ${chartHeight}`} role="img" aria-label={`${metric.label} history`} data-series={metric.key}>
        <line x1={chartPad.left} x2={chartWidth - chartPad.right} y1={chartHeight - chartPad.bottom} y2={chartHeight - chartPad.bottom} className="state-sparkline__baseline" />
        <text x={chartPad.left} y={chartHeight - 2} className="state-sparkline__axis">{metric.unitLabel}</text>
        <path d={path} className="state-sparkline__line" vectorEffect="non-scaling-stroke" />
      </svg>
    </article>
  )
}

function latest(points: StatePoint[], read: (point: StatePoint) => number | null | undefined): number | null {
  for (let index = points.length - 1; index >= 0; index -= 1) {
    const value = finiteOrNull(read(points[index]))
    if (value !== null) return value
  }
  return null
}

function memoryPercent(point: StatePoint): number | null {
  return ratioPercent(point.memoryUsedBytes, point.memoryTotalBytes)
}

function diskPercent(point: StatePoint): number | null {
  return ratioPercent(point.diskUsedBytes, point.diskTotalBytes)
}

function ratioPercent(used: number | null | undefined, total: number | null | undefined): number | null {
  const safeUsed = finiteOrNull(used)
  const safeTotal = finiteOrNull(total)
  if (safeUsed === null || safeTotal === null || safeTotal <= 0) return null
  return Math.max(0, Math.min(100, (safeUsed / safeTotal) * 100))
}

function sumFinite(first: number | null | undefined, second: number | null | undefined): number | null {
  const safeFirst = finiteOrNull(first)
  const safeSecond = finiteOrNull(second)
  if (safeFirst === null && safeSecond === null) return null
  return (safeFirst ?? 0) + (safeSecond ?? 0)
}

function finiteOrNull(value: number | null | undefined): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function yDomain(values: Array<number | null>): { min: number; max: number } {
  const finiteValues = values.filter((value): value is number => value !== null)
  if (finiteValues.length === 0) return { min: 0, max: 1 }
  const max = Math.max(...finiteValues)
  return { min: 0, max: Math.max(1, Math.ceil(max * 1.15)) }
}

function sparklinePath(values: Array<number | null>, domain: { min: number; max: number }): string {
  const plotWidth = chartWidth - chartPad.left - chartPad.right
  const plotHeight = chartHeight - chartPad.top - chartPad.bottom
  const xStep = values.length > 1 ? plotWidth / (values.length - 1) : 0
  const span = domain.max - domain.min || 1
  let open = false

  return values
    .map((value, index) => {
      if (value === null) {
        open = false
        return ''
      }
      const x = chartPad.left + index * xStep
      const y = chartPad.top + (1 - (value - domain.min) / span) * plotHeight
      const command = open ? 'L' : 'M'
      open = true
      return `${command} ${x.toFixed(1)} ${y.toFixed(1)}`
    })
    .filter(Boolean)
    .join(' ')
}
