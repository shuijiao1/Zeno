import type { LatencyPoint } from '../types'
import { buildLatencySeries, yDomain } from '../lib/latencySeries'
import { formatLatency, formatPercent } from '../lib/format'

interface LatencyChartProps {
  points: LatencyPoint[]
}

const width = 960
const height = 320
const pad = { left: 52, right: 24, top: 24, bottom: 44 }

export function LatencyChart({ points }: LatencyChartProps) {
  const series = buildLatencySeries(points)
  const domain = yDomain(points)
  const timestamps = [...new Set(points.map((point) => point.ts))].sort()
  const xStep = timestamps.length > 1 ? (width - pad.left - pad.right) / (timestamps.length - 1) : 0
  const plotHeight = height - pad.top - pad.bottom

  const x = (ts: string) => pad.left + timestamps.indexOf(ts) * xStep
  const y = (value: number) => pad.top + (1 - (value - domain.min) / (domain.max - domain.min)) * plotHeight
  const pathFor = (targetPoints: LatencyPoint[]) => targetPoints
    .filter((point) => point.medianMs !== null)
    .map((point, index) => `${index === 0 ? 'M' : 'L'} ${x(point.ts).toFixed(1)} ${y(point.medianMs!).toFixed(1)}`)
    .join(' ')

  const lastLabel = timestamps.at(-1)?.slice(11, 16) ?? '--:--'
  const firstLabel = timestamps[0]?.slice(11, 16) ?? '--:--'

  return (
    <section className="latency-panel">
      <div className="latency-panel__header">
        <div>
          <p className="eyebrow">Latency</p>
          <h2>多目标延迟图</h2>
        </div>
        <div className="range-tabs" aria-label="range selector mock">
          <button className="is-active">1h</button>
          <button>6h</button>
          <button>24h</button>
          <button>7d</button>
        </div>
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

        {series.map((item) => (
          <path
            key={item.targetId}
            d={pathFor(item.points)}
            fill="none"
            stroke={item.color}
            strokeWidth={item.strokeWidth}
            vectorEffect="non-scaling-stroke"
          />
        ))}

        {points.filter((point) => point.lossPercent > 0).map((point) => (
          <circle
            key={`${point.targetId}-${point.ts}-loss`}
            cx={x(point.ts)}
            cy={point.medianMs === null ? pad.top + plotHeight : y(point.medianMs)}
            r={point.lossPercent >= 100 ? 4 : 3}
            className="loss-marker"
          >
            <title>{`${point.targetName} ${point.ts}: ${formatPercent(point.lossPercent)} loss, ${formatLatency(point.medianMs)}`}</title>
          </circle>
        ))}
      </svg>

      <div className="latency-legend">
        {series.map((item) => (
          <span key={item.targetId}><i style={{ background: item.color }} />{item.targetName}</span>
        ))}
      </div>
    </section>
  )
}
