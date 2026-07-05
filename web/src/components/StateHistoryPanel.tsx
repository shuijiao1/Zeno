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

const chartWidth = 900
const chartHeight = 164
const chartPad = { left: 64, right: 18, top: 16, bottom: 30 }
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
    <section className="monitor-panel state-history-panel" aria-label="agent state history">
      <header className="monitor-heading">
        <div>
          <h3>系统资源历史</h3>
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
          {metrics.map((metric) => <MetricChartCard key={metric.key} metric={metric} timestamps={timestamps} />)}
        </div>
      )}
    </section>
  )
}

function MetricChartCard({ metric, timestamps }: { metric: MetricConfig; timestamps: number[] }) {
  const domain = yDomain(metric.lines.flatMap((line) => line.values), metric.domainMax)
  const timeTicks = stateAxisTicks(timestamps)

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
              <text x={8} y={y + 4} className="state-sparkline__axis" textAnchor="start">{formatAxisValue(value, metric.unitLabel)}</text>
            </g>
          )
        })}
        {timeTicks.map((tick) => (
          <text
            key={tick.index}
            x={xForIndex(tick.index, timestamps.length)}
            y={chartHeight - 8}
            className="state-sparkline__time-axis"
            textAnchor={tick.anchor}
          >
            {formatStateAxisTime(tick.timestamp, timestamps)}
          </text>
        ))}
        {metric.fillArea && metric.lines.map((line) => (
          <path
            key={`${line.key}-area`}
            d={sparklineAreaPath(line.values, domain)}
            className="state-sparkline__area"
            data-series={`${line.key}-area`}
            style={{ fill: line.color }}
          />
        ))}
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
  const plotHeight = chartHeight - chartPad.top - chartPad.bottom
  const span = domain.max - domain.min || 1
  let open = false

  return values
    .map((value, index) => {
      if (value === null) {
        open = false
        return ''
      }
      const x = xForIndex(index, values.length)
      const y = chartPad.top + (1 - (value - domain.min) / span) * plotHeight
      const command = open ? 'L' : 'M'
      open = true
      return `${command} ${x.toFixed(1)} ${y.toFixed(1)}`
    })
    .filter(Boolean)
    .join(' ')
}

function sparklineAreaPath(values: Array<number | null>, domain: { min: number; max: number }): string {
  const plotHeight = chartHeight - chartPad.top - chartPad.bottom
  const span = domain.max - domain.min || 1
  const baseline = chartPad.top + plotHeight
  let segment: Array<{ x: number; y: number }> = []
  const segments: Array<Array<{ x: number; y: number }>> = []

  values.forEach((value, index) => {
    if (value === null) {
      if (segment.length > 0) segments.push(segment)
      segment = []
      return
    }
    const x = xForIndex(index, values.length)
    const y = chartPad.top + (1 - (value - domain.min) / span) * plotHeight
    segment.push({ x, y })
  })
  if (segment.length > 0) segments.push(segment)

  return segments
    .filter((coords) => coords.length > 0)
    .map((coords) => {
      const first = coords[0]
      const last = coords.at(-1)!
      return [
        `M ${first.x.toFixed(1)} ${baseline.toFixed(1)}`,
        ...coords.map((coord) => `L ${coord.x.toFixed(1)} ${coord.y.toFixed(1)}`),
        `L ${last.x.toFixed(1)} ${baseline.toFixed(1)}`,
        'Z',
      ].join(' ')
    })
    .join(' ')
}

function xForIndex(index: number, count: number): number {
  const plotWidth = chartWidth - chartPad.left - chartPad.right
  const xStep = count > 1 ? plotWidth / (count - 1) : 0
  return chartPad.left + index * xStep
}

interface StateAxisTick {
  timestamp: number
  index: number
  anchor: 'start' | 'middle' | 'end'
}

function stateAxisTicks(timestamps: number[]): StateAxisTick[] {
  const valid = timestamps
    .map((timestamp, index) => ({ timestamp, index }))
    .filter((item) => Number.isFinite(item.timestamp))
  if (valid.length === 0) return []
  if (valid.length === 1) return [{ ...valid[0], anchor: 'middle' }]

  const candidates: StateAxisTick[] = [
    { ...valid[0], anchor: 'start' },
    { ...valid.at(-1)!, anchor: 'end' },
  ]
  return candidates.filter((tick, index, all) => all.findIndex((item) => item.index === tick.index) === index)
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
  if (unitLabel === 'load') return value.toFixed(value >= 10 ? 0 : 1)
  if (unitLabel === 'B') return formatBinaryBytes(value)
  return value >= 1024 ? formatBps(value).replace('/s', '') : `${Math.round(value)} ${unitLabel}`
}
