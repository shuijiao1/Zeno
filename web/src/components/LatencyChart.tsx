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

export function LatencyChart({
  points,
  eyebrow = 'Latency',
  title = '多目标延迟图',
  compactHeader = false,
  peakCut = false,
  activeTargetNames = [],
}: LatencyChartProps) {
  const series = buildKulinTargetSeries(points)
  const allRows = buildKulinChartRows(series)
  const baseView = selectKulinChartView(series, allRows, activeTargetNames)
  const rows = peakCut ? applyKulinPeakCut(baseView.rows, baseView.lineKeys) : baseView.rows
  const timestamps = rows.map((row) => row.created_at)
  const xStep = timestamps.length > 1 ? (width - pad.left - pad.right) / (timestamps.length - 1) : 0
  const plotHeight = height - pad.top - pad.bottom
  const domain = yDomainForRows(rows, baseView.lineKeys)
  const packetLossSeries = baseView.showPacketLossArea
    ? series.find((item) => item.targetName === activeTargetNames[0])
    : undefined
  const lossRows = baseView.showPacketLossArea ? rows : []
  const visibleLineKeys = baseView.lineKeys
  const hoverColumns = hoverColumnsForRows(rows, visibleLineKeys, activeTargetNames)
  const legendSeries = activeTargetNames.length > 0
    ? series.filter((item) => activeTargetNames.includes(item.targetName))
    : series

  const x = (createdAt: number) => pad.left + Math.max(0, timestamps.indexOf(createdAt)) * xStep
  const yDelay = (value: number) => pad.top + (1 - (value - domain.min) / (domain.max - domain.min)) * plotHeight
  const yLoss = (value: number) => pad.top + (1 - Math.max(0, Math.min(100, value)) / 100) * plotHeight

  const lastLabel = timestamps.at(-1) ? formatAxisTime(timestamps.at(-1)!) : '--:--'
  const firstLabel = timestamps[0] ? formatAxisTime(timestamps[0]) : '--:--'
  const hitWidth = timestamps.length > 1 ? Math.max(18, xStep) : width - pad.left - pad.right

  return (
    <section className={`latency-panel${compactHeader ? ' is-compact' : ''}`}>
      <div className="latency-panel__header">
        <div>
          <p className="eyebrow">{eyebrow}</p>
          <h2>{title}</h2>
        </div>
        {!compactHeader && (
          <div className="range-tabs" aria-label="range selector mock">
            <button className="is-active">1h</button>
            <button>6h</button>
            <button>24h</button>
            <button>7d</button>
          </div>
        )}
      </div>

      <svg className="latency-chart" viewBox={`0 0 ${width} ${height}`} role="img" aria-label="mock latency chart">
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
        <text x={pad.left} y={height - 12} className="axis-label">{firstLabel}</text>
        <text x={width - pad.right - 40} y={height - 12} className="axis-label">{lastLabel}</text>

        {lossRows.length > 0 && (
          <path
            className="packet-loss-area"
            d={packetLossAreaPath(lossRows, x, yLoss)}
            fill="hsl(45, 100%, 60%)"
            fillOpacity={0.3}
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
          return (
            <g key={column.createdAt} className="latency-hover-column">
              <rect
                className="latency-hover-hit"
                x={Math.max(pad.left, xx - hitWidth / 2)}
                y={pad.top}
                width={Math.min(hitWidth, width - pad.right - Math.max(pad.left, xx - hitWidth / 2))}
                height={plotHeight}
              >
                <title>{column.title}</title>
              </rect>
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
                >
                  <title>{column.title}</title>
                </circle>
              ))}
            </g>
          )
        })}
      </svg>

      <div className="latency-legend">
        {legendSeries.map((item, index) => (
          <span key={item.targetId}><i style={{ background: palette[(series.findIndex((seriesItem) => seriesItem.targetId === item.targetId) >= 0 ? series.findIndex((seriesItem) => seriesItem.targetId === item.targetId) : index) % palette.length] }} />{item.targetName}</span>
        ))}
        {baseView.showPacketLossArea && packetLossSeries && (
          <span><i style={{ background: 'hsl(45, 100%, 60%)' }} />{packetLossSeries.targetName} 丢包 {formatPercent(avgPacketLoss(lossRows))}</span>
        )}
      </div>
    </section>
  )
}

function linePath(rows: KulinChartRow[], key: string, x: (createdAt: number) => number, y: (value: number) => number): string {
  let hasOpenSegment = false
  return rows
    .map((row) => {
      const value = rowNumber(row, key)
      if (value === null) return ''
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
  const max = Math.max(...values)
  return { min: 0, max: Math.max(1, Math.ceil(max * 1.2)) }
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

function formatAxisTime(createdAt: number): string {
  const date = new Date(createdAt)
  if (Number.isNaN(date.getTime())) return '--:--'
  return `${date.getHours()}:${date.getMinutes().toString().padStart(2, '0')}`
}
