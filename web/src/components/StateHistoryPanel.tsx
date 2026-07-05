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
  fillArea?: boolean
  lines: MetricLine[]
}

const plotWidth = 640
const plotHeight = 112
const stateRangeOptions = [
  { value: '1h', label: '实时' },
  { value: '1d', label: '1 天' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
]

export function StateHistoryPanel({ points, range, loading = false, error, onRangeChange = () => {} }: StateHistoryPanelProps) {
  const sampleCount = points.length
  const latestCpu = latest(points, (point) => point.cpuPercent)
  const latestMemory = latest(points, memoryPercent)
  const latestDisk = latest(points, diskPercent)
  const latestInSpeed = latest(points, (point) => point.netInSpeedBps)
  const latestOutSpeed = latest(points, (point) => point.netOutSpeedBps)
  const latestSwap = latest(points, swapPercent)
  const latestProcessCount = latest(points, (point) => point.processCount)
  const latestTcpConnectionCount = latest(points, (point) => point.tcpConnectionCount)
  const latestUdpConnectionCount = latest(points, (point) => point.udpConnectionCount)
  const timestamps = points.map((point) => Date.parse(point.ts))

  const metrics: MetricConfig[] = [
    {
      key: 'cpu',
      label: 'CPU',
      value: formatPercent(latestCpu),
      tone: 'green',
      unitLabel: '%',
      domainMax: 100,
      fillArea: true,
      lines: [{ key: 'cpu', label: 'CPU', values: points.map((point) => finiteOrNull(point.cpuPercent)), color: '#22c55e' }],
    },
    {
      key: 'memory',
      label: '内存 / Swap',
      value: <><MetricValue label="内存" value={formatPercent(latestMemory)} /><MetricValue label="Swap" value={formatPercent(latestSwap)} /></>,
      tone: 'blue',
      unitLabel: '%',
      domainMax: 100,
      fillArea: true,
      lines: [
        { key: 'memory', label: '内存', values: points.map(memoryPercent), color: '#2563eb' },
        { key: 'swap', label: 'Swap', values: points.map(swapPercent), color: '#0ea5e9' },
      ],
    },
    {
      key: 'disk',
      label: '磁盘',
      value: formatPercent(latestDisk),
      tone: 'purple',
      unitLabel: '%',
      domainMax: 100,
      fillArea: true,
      lines: [{ key: 'disk', label: '磁盘', values: points.map(diskPercent), color: '#9333ea' }],
    },
    {
      key: 'network',
      label: '网络速率',
      value: <><MetricValue label="上传" value={formatBps(latestOutSpeed)} ariaLabel={`↑${formatBps(latestOutSpeed)}`} /><MetricValue label="下载" value={formatBps(latestInSpeed)} ariaLabel={`↓${formatBps(latestInSpeed)}`} /></>,
      tone: 'orange',
      unitLabel: 'B/s',
      lines: [
        { key: 'net-out', label: '上传', values: points.map((point) => finiteOrNull(point.netOutSpeedBps)), color: '#f97316' },
        { key: 'net-in', label: '下载', values: points.map((point) => finiteOrNull(point.netInSpeedBps)), color: '#06b6d4' },
      ],
    },
    {
      key: 'processes',
      label: '进程数',
      value: latestProcessCount !== null ? Math.round(latestProcessCount) : '--',
      tone: 'purple',
      unitLabel: 'count',
      fillArea: true,
      lines: [{ key: 'processes', label: '进程', values: points.map((point) => finiteOrNull(point.processCount)), color: '#a855f7' }],
    },
    {
      key: 'connections',
      label: 'TCP / UDP',
      value: <><MetricValue label="TCP" value={latestTcpConnectionCount !== null ? String(Math.round(latestTcpConnectionCount)) : '--'} /><MetricValue label="UDP" value={latestUdpConnectionCount !== null ? String(Math.round(latestUdpConnectionCount)) : '--'} /></>,
      tone: 'orange',
      unitLabel: 'count',
      lines: [
        { key: 'tcp', label: 'TCP', values: points.map((point) => finiteOrNull(point.tcpConnectionCount)), color: '#ec4899' },
        { key: 'udp', label: 'UDP', values: points.map((point) => finiteOrNull(point.udpConnectionCount)), color: '#38bdf8' },
      ],
    },
  ]

  return (
    <section className="monitor-panel resource-history-panel" aria-label="agent state history">
      <header className="resource-history-header">
        <h3>系统资源历史</h3>
        <div className="detail-range-row resource-range-row" aria-label="resource history range selector">
          {stateRangeOptions.map((option) => (
            <button key={option.value} type="button" className={range === option.value ? 'is-active' : ''} onClick={() => onRangeChange(option.value)}>{option.label}</button>
          ))}
        </div>
      </header>

      {loading && <div className="detail-state">正在读取系统资源…</div>}
      {error && <div className="detail-state is-error">系统资源读取失败：{error}</div>}
      {!loading && !error && sampleCount === 0 && <div className="detail-state">暂无系统资源历史</div>}

      {!loading && !error && sampleCount > 0 && (
        <div className="resource-history-grid">
          {metrics.map((metric) => <ResourceMetricCard key={metric.key} metric={metric} timestamps={timestamps} />)}
        </div>
      )}
    </section>
  )
}

function ResourceMetricCard({ metric, timestamps }: { metric: MetricConfig; timestamps: number[] }) {
  const domain = yDomain(metric.lines.flatMap((line) => line.values), metric.domainMax)
  const yTicks = yAxisTicks(domain, metric.unitLabel)
  const timeTicks = stateAxisTicks(timestamps)

  return (
    <article className={`resource-card tone-${metric.tone}`}>
      <header className="resource-card-header">
        <div className="resource-card-title-block">
          <p className="resource-card-title">{metric.label}</p>
          {metric.lines.length > 1 && (
            <span className="resource-chart-legend">
              {metric.lines.map((line) => <em key={line.key} style={{ color: line.color }}>{line.label}</em>)}
            </span>
          )}
        </div>
        <strong className="resource-card-value">{metric.value}</strong>
      </header>

      <div className="resource-chart-frame">
        <div className="resource-y-axis" aria-hidden="true">
          {yTicks.map((tick) => <span key={tick}>{tick}</span>)}
        </div>
        <div className="resource-chart-plot">
          <svg className="resource-chart-svg" viewBox={`0 0 ${plotWidth} ${plotHeight}`} preserveAspectRatio="none" role="img" aria-label={`${metric.label} history`}>
            {[0, 0.5, 1].map((ratio) => (
              <line key={ratio} x1={0} x2={plotWidth} y1={ratio * plotHeight} y2={ratio * plotHeight} className="resource-grid-line" vectorEffect="non-scaling-stroke" />
            ))}
            {metric.fillArea && metric.lines.map((line) => (
              <path
                key={`${line.key}-area`}
                d={chartAreaPath(line.values, domain)}
                className="resource-chart-area"
                data-series={`${line.key}-area`}
                style={{ fill: line.color }}
              />
            ))}
            {metric.lines.map((line) => (
              <path
                key={line.key}
                d={chartLinePath(line.values, domain)}
                className="resource-chart-line"
                data-series={line.key}
                style={{ stroke: line.color }}
                vectorEffect="non-scaling-stroke"
              />
            ))}
          </svg>
        </div>
        <div className="resource-x-spacer" aria-hidden="true" />
        <div className="resource-x-axis" aria-hidden="true">
          {timeTicks.map((tick) => <span key={tick.index}>{formatStateAxisTime(tick.timestamp, timestamps)}</span>)}
        </div>
      </div>
    </article>
  )
}

function MetricValue({ label, value, ariaLabel }: { label: string; value: string; ariaLabel?: string }) {
  return <span aria-label={ariaLabel ?? `${label} ${value}`}><span className="metric-inline-label">{label}</span> {value}</span>
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

function yDomain(values: Array<number | null>, forcedMax?: number): { min: number; max: number } {
  const finiteValues = values.filter((value): value is number => value !== null)
  if (finiteValues.length === 0) return { min: 0, max: forcedMax ?? 1 }
  const max = Math.max(...finiteValues)
  return { min: 0, max: Math.max(1, forcedMax ?? Math.ceil(max * 1.15)) }
}

function chartLinePath(values: Array<number | null>, domain: { min: number; max: number }): string {
  const span = domain.max - domain.min || 1
  let open = false

  return values
    .map((value, index) => {
      if (value === null) {
        open = false
        return ''
      }
      const x = chartX(index, values.length)
      const y = plotHeight - ((value - domain.min) / span) * plotHeight
      const command = open ? 'L' : 'M'
      open = true
      return `${command} ${x.toFixed(1)} ${y.toFixed(1)}`
    })
    .filter(Boolean)
    .join(' ')
}

function chartAreaPath(values: Array<number | null>, domain: { min: number; max: number }): string {
  const span = domain.max - domain.min || 1
  let segment: Array<{ x: number; y: number }> = []
  const segments: Array<Array<{ x: number; y: number }>> = []

  values.forEach((value, index) => {
    if (value === null) {
      if (segment.length > 0) segments.push(segment)
      segment = []
      return
    }
    segment.push({
      x: chartX(index, values.length),
      y: plotHeight - ((value - domain.min) / span) * plotHeight,
    })
  })
  if (segment.length > 0) segments.push(segment)

  return segments
    .map((coords) => {
      const first = coords[0]
      const last = coords.at(-1)!
      return [
        `M ${first.x.toFixed(1)} ${plotHeight}`,
        ...coords.map((coord) => `L ${coord.x.toFixed(1)} ${coord.y.toFixed(1)}`),
        `L ${last.x.toFixed(1)} ${plotHeight}`,
        'Z',
      ].join(' ')
    })
    .join(' ')
}

function chartX(index: number, count: number): number {
  if (count <= 1) return 0
  return (index / (count - 1)) * plotWidth
}

function yAxisTicks(domain: { min: number; max: number }, unitLabel: string): string[] {
  return [domain.max, domain.min + (domain.max - domain.min) / 2, domain.min].map((value) => formatAxisValue(value, unitLabel))
}

interface StateAxisTick {
  timestamp: number
  index: number
}

function stateAxisTicks(timestamps: number[]): StateAxisTick[] {
  const valid = timestamps
    .map((timestamp, index) => ({ timestamp, index }))
    .filter((item) => Number.isFinite(item.timestamp))
  if (valid.length === 0) return []
  if (valid.length === 1) return [valid[0]]
  return [valid[0], valid.at(-1)!]
}

function formatStateAxisTime(timestamp: number, timestamps: number[]): string {
  if (!Number.isFinite(timestamp)) return '--'
  const latest = [...timestamps].reverse().find(Number.isFinite) ?? timestamp
  const diffMs = Math.max(0, latest - timestamp)
  return formatRelativeDuration(diffMs)
}

function formatRelativeDuration(diffMs: number): string {
  const totalSeconds = Math.floor(diffMs / 1000)
  const days = Math.floor(totalSeconds / 86_400)
  const hours = Math.floor((totalSeconds % 86_400) / 3_600)
  const minutes = Math.floor((totalSeconds % 3_600) / 60)
  if (days > 0) return `${days}d`
  if (hours > 0) return `${hours}h`
  if (minutes > 0) return `${minutes}m`
  return `${Math.max(totalSeconds, 0)}s`
}

function formatAxisValue(value: number, unitLabel: string): string {
  if (unitLabel === '%') return `${Math.round(value)}%`
  if (unitLabel === 'count') return `${Math.round(value)}`
  return value >= 1024 ? formatBps(value).replace('/s', '') : `${Math.round(value)} ${unitLabel}`
}
