import { useMemo, useState } from 'react'
import type { LatencyPoint } from '../types'
import {
  applyKulinPeakCut,
  buildKulinChartRows,
  buildKulinTargetSeries,
  selectKulinChartView,
  type KulinChartRow,
  type KulinTargetSeries,
} from '../lib/kulinLatencyChart'
import { formatPercent } from '../lib/format'

interface LatencyChartProps {
  points: LatencyPoint[]
  eyebrow?: string
  title?: string
  compactHeader?: boolean
  peakCut?: boolean
  activeTargetNames?: string[]
}

const width = 960
const height = 320
const pad = { left: 52, right: 24, top: 24, bottom: 44 }
const palette = ['#22c55e', '#38bdf8', '#f59e0b', '#a78bfa', '#fb7185', '#14b8a6', '#84cc16', '#f97316', '#06b6d4', '#e879f9']
const packetLossColor = '#94a3b8'

export function LatencyChart({
  points,
  eyebrow = 'Latency',
  title = '多目标延迟图',
  compactHeader = false,
  peakCut = false,
  activeTargetNames = [],
}: LatencyChartProps) {
  const activeTargetKey = activeTargetNames.join('\u0000')
  const series = useMemo(() => buildKulinTargetSeries(points), [points])
  const allRows = useMemo(() => buildKulinChartRows(series), [series])
  const baseView = useMemo(() => selectKulinChartView(series, allRows, activeTargetNames), [series, allRows, activeTargetKey])
  const rows = useMemo(() => (peakCut ? applyKulinPeakCut(baseView.rows, baseView.lineKeys) : baseView.rows), [baseView, peakCut])
  const timestamps = useMemo(() => rows.map((row) => row.created_at), [rows])
  const xStep = timestamps.length > 1 ? (width - pad.left - pad.right) / (timestamps.length - 1) : 0
  const xByTimestamp = useMemo(() => new Map(timestamps.map((timestamp, index) => [timestamp, pad.left + index * xStep])), [timestamps, xStep])
  const axisTicks = useMemo(() => axisTicksForTimestamps(timestamps), [timestamps])
  const plotHeight = height - pad.top - pad.bottom
  const domain = useMemo(() => yDomainForRows(rows, baseView.lineKeys), [rows, baseView.lineKeys])
  const packetLossSeries = baseView.showPacketLossArea
    ? series.find((item) => item.targetName === activeTargetNames[0])
    : undefined
  const lossRows = baseView.showPacketLossArea ? rows : []
  const visibleLineKeys = baseView.lineKeys
  const hoverColumns = useMemo(() => hoverColumnsForRows(rows, visibleLineKeys, activeTargetNames), [rows, visibleLineKeys, activeTargetKey])
  const legendSeries = useMemo(() => (activeTargetNames.length > 0
    ? series.filter((item) => activeTargetNames.includes(item.targetName))
    : series), [series, activeTargetKey])
  const [hoverColumn, setHoverColumn] = useState<HoverColumn | null>(null)
  const hoverColumnCreatedAt = hoverColumn?.createdAt ?? null

  const x = (createdAt: number) => xByTimestamp.get(createdAt) ?? pad.left
  const yDelay = (value: number) => pad.top + (1 - (value - domain.min) / (domain.max - domain.min)) * plotHeight
  const yLoss = (value: number) => pad.top + (1 - Math.max(0, Math.min(100, value)) / 100) * plotHeight

  const hitWidth = timestamps.length > 1 ? Math.max(18, xStep) : width - pad.left - pad.right

  return (
    <section className={`latency-panel${compactHeader ? ' is-compact' : ''}`}>
      <div className="latency-panel__header">
        <div>
          <p className="eyebrow">{eyebrow}</p>
          <h2>{title}</h2>
        </div>
        {!compactHeader && (
          <div className="range-tabs" aria-label="range selector">
            <button className="is-active">1h</button>
            <button>6h</button>
            <button>24h</button>
            <button>7d</button>
          </div>
        )}
      </div>

      <svg className="latency-chart" viewBox={`0 0 ${width} ${height}`} role="img" aria-label="latency chart" onMouseLeave={() => setHoverColumn(null)}>
        {[0, 0.25, 0.5, 0.75, 1].map((ratio) => {
          const yy = pad.top + ratio * plotHeight
          const value = domain.max - ratio * (domain.max - domain.min)
          return (
            <g key={ratio}>
              <line x1={pad.left} x2={width - pad.right} y1={yy} y2={yy} className="grid-line" />
              <text x={12} y={yy + 4} className="axis-label">{Math.round(value)}ms</text>
            </g>
          )
        })}
        {axisTicks.map((tick) => {
          const xx = x(tick)
          return (
            <text
              key={tick}
              x={Math.min(width - pad.right, Math.max(pad.left, xx))}
              y={height - 12}
              className="axis-label"
              textAnchor={axisTickAnchor(xx)}
            >
              {formatAxisTime(tick, timestamps)}
            </text>
          )
        })}

        {lossRows.length > 0 && (
          <path
            className="packet-loss-area"
            d={packetLossAreaPath(lossRows, x, yLoss)}
            fill={packetLossColor}
            fillOpacity={0.18}
            stroke="none"
          />
        )}

        {visibleLineKeys.map((key) => (
          <path
            key={key}
            d={linePath(rows, key, x, yDelay)}
            fill="none"
            stroke={palette[paletteIndexForKey(series, key, activeTargetNames) % palette.length]}
            strokeWidth={1}
            vectorEffect="non-scaling-stroke"
          />
        ))}

        {hoverColumns.map((column) => {
          const xx = x(column.createdAt)
          const isActive = hoverColumnCreatedAt === column.createdAt
          return (
            <g key={column.createdAt} className={`latency-hover-column${isActive ? ' is-active' : ''}`}>
              <rect
                className="latency-hover-hit"
                x={Math.max(pad.left, xx - hitWidth / 2)}
                y={pad.top}
                width={Math.min(hitWidth, width - pad.right - Math.max(pad.left, xx - hitWidth / 2))}
                height={plotHeight}
                aria-label={column.title}
                onMouseEnter={() => setHoverColumn(column)}
                onMouseMove={() => setHoverColumn(column)}
              />
              <line
                className="latency-hover-guide"
                x1={xx}
                x2={xx}
                y1={pad.top}
                y2={height - pad.bottom}
                vectorEffect="non-scaling-stroke"
              />
              {column.points.map((point) => (
                <circle
                  key={`${point.key}-${column.createdAt}`}
                  className="latency-hover-point"
                  cx={xx}
                  cy={yDelay(point.delay)}
                  r={5}
                  fill={palette[paletteIndexForKey(series, point.key, activeTargetNames) % palette.length]}
                />
              ))}
            </g>
          )
        })}
        {hoverColumn && (
          <LatencyTooltip
            column={hoverColumn}
            series={series}
            activeTargetNames={activeTargetNames}
            x={x(hoverColumn.createdAt)}
          />
        )}
      </svg>

      <div className="latency-legend">
        {legendSeries.map((item, index) => (
          <span key={item.targetId}><i style={{ background: palette[(series.findIndex((seriesItem) => seriesItem.targetId === item.targetId) >= 0 ? series.findIndex((seriesItem) => seriesItem.targetId === item.targetId) : index) % palette.length] }} />{item.targetName}</span>
        ))}
        {baseView.showPacketLossArea && packetLossSeries && (
          <span><i style={{ background: packetLossColor }} />{packetLossSeries.targetName} 丢包 {formatPercent(avgPacketLoss(lossRows))}</span>
        )}
      </div>
    </section>
  )
}

function LatencyTooltip({ column, series, activeTargetNames, x: tooltipAnchorX }: { column: HoverColumn; series: KulinTargetSeries[]; activeTargetNames: string[]; x: number }) {
  const tooltipWidth = 232
  const rowHeight = 18
  const tooltipHeight = 34 + column.points.length * rowHeight + 10
  const tooltipX = Math.max(pad.left, Math.min(width - pad.right - tooltipWidth, tooltipAnchorX + 12))
  const tooltipY = Math.max(pad.top + 4, Math.min(height - pad.bottom - tooltipHeight, pad.top + 8))

  return (
    <g className="latency-chart-tooltip" transform={`translate(${tooltipX} ${tooltipY})`}>
      <rect width={tooltipWidth} height={tooltipHeight} rx={12} ry={12} />
      <text x={12} y={20} className="latency-tooltip-time">{formatTooltipTime(column.createdAt)}</text>
      {column.points.map((point, index) => (
        <g key={`${point.key}-${column.createdAt}`} transform={`translate(12 ${34 + index * rowHeight})`}>
          <circle cx={4} cy={-4} r={3.5} fill={palette[paletteIndexForKey(series, point.key, activeTargetNames) % palette.length]} />
          <text x={14} y={0} className="latency-tooltip-label">{point.label}</text>
          <text x={tooltipWidth - 24} y={0} textAnchor="end" className="latency-tooltip-value">{formatLatencyValue(point.delay)}</text>
        </g>
      ))}
    </g>
  )
}

function linePath(rows: KulinChartRow[], key: string, x: (createdAt: number) => number, y: (value: number) => number): string {
  let hasOpenSegment = false
  return rows
    .map((row) => {
      const value = rowNumber(row, key)
      if (value === null) {
        hasOpenSegment = false
        return ''
      }
      const command = hasOpenSegment ? 'L' : 'M'
      hasOpenSegment = true
      return `${command} ${x(row.created_at).toFixed(1)} ${y(value).toFixed(1)}`
    })
    .filter(Boolean)
    .join(' ')
}

function packetLossAreaPath(rows: KulinChartRow[], x: (createdAt: number) => number, yLoss: (value: number) => number): string {
  const coords = rows
    .map((row) => {
      const value = rowNumber(row, 'packet_loss')
      return value === null ? null : { x: x(row.created_at), y: yLoss(value) }
    })
    .filter((coord): coord is { x: number; y: number } => coord !== null)

  if (coords.length === 0) return ''
  const baseline = pad.top + (height - pad.top - pad.bottom)
  return [
    `M ${coords[0].x.toFixed(1)} ${baseline.toFixed(1)}`,
    ...coords.map((coord) => `L ${coord.x.toFixed(1)} ${coord.y.toFixed(1)}`),
    `L ${coords.at(-1)!.x.toFixed(1)} ${baseline.toFixed(1)}`,
    'Z',
  ].join(' ')
}

function yDomainForRows(rows: KulinChartRow[], keys: string[]): { min: number; max: number } {
  const values = rows.flatMap((row) => keys.map((key) => rowNumber(row, key)).filter((value): value is number => value !== null))
  if (values.length === 0) return { min: 0, max: 1 }
  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min
  if (span <= 0) {
    const padding = Math.max(0.5, Math.abs(max) * 0.05)
    return { min: Math.max(0, min - padding), max: max + padding }
  }
  const padding = Math.max(span * 0.15, max * 0.002, 0.05)
  return { min: Math.max(0, min - padding), max: max + padding }
}

function rowNumber(row: KulinChartRow, key: string): number | null {
  const value = row[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

interface HoverPoint {
  key: string
  label: string
  delay: number
}

interface HoverColumn {
  createdAt: number
  title: string
  points: HoverPoint[]
}

function hoverColumnsForRows(rows: KulinChartRow[], keys: string[], activeTargetNames: string[]): HoverColumn[] {
  return rows
    .map((row) => {
      const points = keys
        .map((key) => {
          const delay = rowNumber(row, key)
          if (delay === null) return null
          return {
            key,
            label: key === 'avg_delay' ? (activeTargetNames[0] ?? '延迟') : key,
            delay,
          }
        })
        .filter((point): point is HoverPoint => point !== null)
      if (points.length === 0) return null
      const title = [
        formatTooltipTime(row.created_at),
        ...points.map((point) => `${point.label} · ${formatLatencyValue(point.delay)}`),
      ].join('\n')
      return { createdAt: row.created_at, title, points }
    })
    .filter((column): column is HoverColumn => column !== null)
}

function paletteIndexForKey(series: KulinTargetSeries[], key: string, activeTargetNames: string[]): number {
  const targetName = key === 'avg_delay' ? activeTargetNames[0] : key
  const index = series.findIndex((item) => item.targetName === targetName)
  return Math.max(index, 0)
}

function formatLatencyValue(value: number): string {
  return `${Number.isInteger(value) ? value.toFixed(0) : value.toFixed(2)}ms`
}

function formatTooltipTime(createdAt: number): string {
  const date = new Date(createdAt)
  if (Number.isNaN(date.getTime())) return '--:--'
  return date.toLocaleString('zh-CN', { hour12: false })
}

function avgPacketLoss(rows: KulinChartRow[]): number {
  const values = rows.map((row) => rowNumber(row, 'packet_loss')).filter((value): value is number => value !== null)
  if (values.length === 0) return 0
  return values.reduce((sum, value) => sum + value, 0) / values.length
}

function axisTicksForTimestamps(timestamps: number[]): number[] {
  if (timestamps.length <= 1) return timestamps
  if (timestamps.length < 6) return [timestamps[0], timestamps.at(-1)!]

  const start = timestamps[0]
  const end = timestamps.at(-1)!
  const spanHours = (end - start) / 3_600_000
  const lastIndex = timestamps.length - 1
  if (spanHours <= 12) {
    const ticks = timestamps.filter((timestamp, index) => {
      const date = new Date(timestamp)
      return index === 0 || index === lastIndex || date.getMinutes() === 0
    })
    return thinTicks(ticks, 10)
  }

  if (spanHours <= 36) {
    const ticks = timestamps.filter((timestamp, index) => {
      if (index === 0 || index === lastIndex) return false
      const date = new Date(timestamp)
      return date.getMinutes() === 0 && date.getHours() % 2 === 0
    })
    return ticks.length >= 2 ? ticks : [start, end]
  }

  if (spanHours <= 24 * 10) {
    const ticks = timestamps.filter((timestamp, index) => {
      if (index === 0 || index === lastIndex) return false
      const date = new Date(timestamp)
      return date.getHours() % 12 === 0 && date.getMinutes() === 0
    })
    return thinTicks(ticks.length >= 2 ? ticks : [start, end], 12)
  }

  return thinTicks(timestamps, 8)
}

function thinTicks(ticks: number[], maxTicks: number): number[] {
  if (ticks.length <= maxTicks) return ticks
  const step = Math.ceil(ticks.length / maxTicks)
  return ticks.filter((_, index) => index % step === 0)
}

function axisTickAnchor(x: number): 'start' | 'middle' | 'end' {
  if (x <= pad.left + 8) return 'start'
  if (x >= width - pad.right - 8) return 'end'
  return 'middle'
}

function formatAxisTime(createdAt: number, timestamps: number[]): string {
  const date = new Date(createdAt)
  if (Number.isNaN(date.getTime())) return '--:--'
  const start = timestamps[0] ?? createdAt
  const end = timestamps.at(-1) ?? createdAt
  const spanHours = (end - start) / 3_600_000
  const time = `${date.getHours()}:${date.getMinutes().toString().padStart(2, '0')}`
  if (spanHours > 36) return `${date.getMonth() + 1}/${date.getDate()} ${time}`
  return time
}
