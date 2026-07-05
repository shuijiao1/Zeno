import type { ReactNode } from 'react'
import type { StatePoint } from '../types'
import { formatBps, formatPercent } from '../lib/format'

interface StateHistoryPanelProps {
  points: StatePoint[]
  range: string
  loading?: boolean
  error?: string
  onRangeChange?: (range: string) => void
}

interface MetricLine {
  key: string
  label: string
  values: Array<number | null>
  color: string
}

interface MetricConfig {
  key: string
  label: string
  value: ReactNode
  tone: 'green' | 'blue' | 'purple' | 'orange'
  unitLabel: string
  domainMax?: number
  lines: MetricLine[]
}

const chartWidth = 900
const chartHeight = 180
const chartPad = { left: 48, right: 18, top: 18, bottom: 34 }
const stateRangeOptions = [
  { value: '1h', label: '实时' },
  { value: '1d', label: '1 天' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
]

export function StateHistoryPanel({ points, range, loading = false, error, onRangeChange = () => {} }: StateHistoryPanelProps) {
  const rangeLabel = stateRangeOptions.find((option) => option.value === range)?.label ?? range
  const sampleCount = points.length
  const latestCpu = latest(points, (point) => point.cpuPercent)
  const latestMemory = latest(points, memoryPercent)
  const latestDisk = latest(points, diskPercent)
  const latestInSpeed = latest(points, (point) => point.netInSpeedBps)
  const latestOutSpeed = latest(points, (point) => point.netOutSpeedBps)
  const latestInTotal = latest(points, (point) => point.netInTotalBytes)
  const latestOutTotal = latest(points, (point) => point.netOutTotalBytes)
  const latestLoad1 = latest(points, (point) => point.load1)
  const latestLoad5 = latest(points, (point) => point.load5)
  const latestLoad15 = latest(points, (point) => point.load15)
  const latestSwap = latest(points, swapPercent)
  const latestProcessCount = latest(points, (point) => point.processCount)
  const latestTcpConnectionCount = latest(points, (point) => point.tcpConnectionCount)

  const metrics: MetricConfig[] = [
    {
      key: 'cpu',
      label: 'CPU',
      value: formatPercent(latestCpu),
      tone: 'green',
      unitLabel: '%',
      domainMax: 100,
      lines: [{ key: 'cpu', label: 'CPU', values: points.map((point) => finiteOrNull(point.cpuPercent)), color: '#22c55e' }],
    },
    {
      key: 'memory',
      label: '内存',
      value: formatPercent(latestMemory),
      tone: 'blue',
      unitLabel: '%',
      domainMax: 100,
      lines: [{ key: 'memory', label: '内存', values: points.map(memoryPercent), color: '#2563eb' }],
    },
    {
      key: 'disk',
      label: '磁盘',
      value: formatPercent(latestDisk),
      tone: 'purple',
      unitLabel: '%',
      domainMax: 100,
      lines: [{ key: 'disk', label: '磁盘', values: points.map(diskPercent), color: '#9333ea' }],
    },
    {
      key: 'network',
      label: '网络速率',
      value: <><span>↑{formatBps(latestOutSpeed)}</span><span>↓{formatBps(latestInSpeed)}</span></>,
      tone: 'orange',
      unitLabel: 'B/s',
      lines: [
        { key: 'net-out', label: '上传', values: points.map((point) => finiteOrNull(point.netOutSpeedBps)), color: '#f97316' },
        { key: 'net-in', label: '下载', values: points.map((point) => finiteOrNull(point.netInSpeedBps)), color: '#06b6d4' },
      ],
    },
    {
      key: 'load',
      label: '系统负载',
      value: latestLoad1 !== null && latestLoad5 !== null && latestLoad15 !== null ? `${formatFixed(latestLoad1, 2)} / ${formatFixed(latestLoad5, 2)} / ${formatFixed(latestLoad15, 2)}` : '--',
      tone: 'green',
      unitLabel: 'load',
      lines: [
        { key: 'load1', label: '1m', values: points.map((point) => finiteOrNull(point.load1)), color: '#22c55e' },
        { key: 'load5', label: '5m', values: points.map((point) => finiteOrNull(point.load5)), color: '#84cc16' },
        { key: 'load15', label: '15m', values: points.map((point) => finiteOrNull(point.load15)), color: '#14b8a6' },
      ],
    },
    {
      key: 'swap',
      label: 'Swap',
      value: formatPercent(latestSwap),
      tone: 'blue',
      unitLabel: '%',
      domainMax: 100,
      lines: [{ key: 'swap', label: 'Swap', values: points.map(swapPercent), color: '#0ea5e9' }],
    },
    {
      key: 'connections',
      label: '进程 / TCP',
      value: <><span>进程 {latestProcessCount !== null ? Math.round(latestProcessCount) : '--'}</span><span>TCP {latestTcpConnectionCount !== null ? Math.round(latestTcpConnectionCount) : '--'}</span></>,
      tone: 'purple',
      unitLabel: 'count',
      lines: [
        { key: 'processes', label: '进程', values: points.map((point) => finiteOrNull(point.processCount)), color: '#a855f7' },
        { key: 'tcp', label: 'TCP', values: points.map((point) => finiteOrNull(point.tcpConnectionCount)), color: '#ec4899' },
      ],
    },
    {
      key: 'traffic-total',
      label: '网络累计',
      value: <><span>↑{formatBinaryBytes(latestOutTotal)}</span><span>↓{formatBinaryBytes(latestInTotal)}</span></>,
      tone: 'orange',
      unitLabel: 'B',
      lines: [
        { key: 'net-out-total', label: '上传累计', values: points.map((point) => finiteOrNull(point.netOutTotalBytes)), color: '#fb923c' },
        { key: 'net-in-total', label: '下载累计', values: points.map((point) => finiteOrNull(point.netInTotalBytes)), color: '#38bdf8' },
      ],
    },
  ]

  return (
    <section className="monitor-panel state-history-panel" aria-label="agent state history">
      <header className="monitor-heading">
        <div>
          <h3>系统资源历史</h3>
          <p>{rangeLabel} · {sampleCount} 个状态采样</p>
        </div>
        <div className="monitor-heading-actions">
          <div className="detail-range-row state-range-row" aria-label="resource history range selector">
            {stateRangeOptions.map((option) => (
              <button key={option.value} type="button" className={range === option.value ? 'is-active' : ''} onClick={() => onRangeChange(option.value)}>{option.label}</button>
            ))}
          </div>
        </div>
      </header>

      {loading && <div className="detail-state">正在读取系统资源…</div>}
      {error && <div className="detail-state is-error">系统资源读取失败：{error}</div>}
      {!loading && !error && sampleCount === 0 && <div className="detail-state">暂无系统资源历史</div>}

      {!loading && !error && sampleCount > 0 && (
        <div className="state-history-stack">
          {metrics.map((metric) => <MetricChartCard key={metric.key} metric={metric} />)}
        </div>
      )}
    </section>
  )
}

function MetricChartCard({ metric }: { metric: MetricConfig }) {
  const domain = yDomain(metric.lines.flatMap((line) => line.values), metric.domainMax)

  return (
    <article className={`state-history-chart-card tone-${metric.tone}`}>
      <div className="state-history-card__meta">
        <div>
          <p>{metric.label}</p>
          {metric.lines.length > 1 && (
            <span className="state-chart-legend">
              {metric.lines.map((line) => <em key={line.key} style={{ color: line.color }}>{line.label}</em>)}
            </span>
          )}
        </div>
        <strong>{metric.value}</strong>
      </div>
      <svg className="state-sparkline state-sparkline--large" viewBox={`0 0 ${chartWidth} ${chartHeight}`} role="img" aria-label={`${metric.label} history`}>
        {[0, 0.5, 1].map((ratio) => {
          const y = chartPad.top + ratio * (chartHeight - chartPad.top - chartPad.bottom)
          const value = domain.max - ratio * (domain.max - domain.min)
          return (
            <g key={ratio}>
              <line x1={chartPad.left} x2={chartWidth - chartPad.right} y1={y} y2={y} className="state-sparkline__baseline" />
              <text x={8} y={y + 4} className="state-sparkline__axis">{formatAxisValue(value, metric.unitLabel)}</text>
            </g>
          )
        })}
        {metric.lines.map((line) => (
          <path
            key={line.key}
            d={sparklinePath(line.values, domain)}
            className="state-sparkline__line"
            data-series={line.key}
            style={{ stroke: line.color }}
            vectorEffect="non-scaling-stroke"
          />
        ))}
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

function swapPercent(point: StatePoint): number | null {
  return ratioPercent(point.swapUsedBytes, point.swapTotalBytes)
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

function finiteOrNull(value: number | null | undefined): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function formatFixed(value: number, digits: number): string {
  return value.toFixed(digits)
}

function formatBinaryBytes(value: number | null): string {
  if (value === null) return '--'
  if (value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  return `${size.toFixed(unit === 0 ? 0 : 2)} ${units[unit]}`
}

function yDomain(values: Array<number | null>, forcedMax?: number): { min: number; max: number } {
  const finiteValues = values.filter((value): value is number => value !== null)
  if (finiteValues.length === 0) return { min: 0, max: forcedMax ?? 1 }
  const max = Math.max(...finiteValues)
  return { min: 0, max: Math.max(1, forcedMax ?? Math.ceil(max * 1.15)) }
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

function formatAxisValue(value: number, unitLabel: string): string {
  if (unitLabel === '%') return `${Math.round(value)}%`
  if (unitLabel === 'count') return `${Math.round(value)}`
  if (unitLabel === 'load') return value.toFixed(value >= 10 ? 0 : 1)
  if (unitLabel === 'B') return formatBinaryBytes(value)
  return value >= 1024 ? formatBps(value).replace('/s', '') : `${Math.round(value)} ${unitLabel}`
}
