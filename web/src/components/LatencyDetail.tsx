import { useMemo, useState } from 'react'
import type { HomeCardNode, LatencyPoint } from '../types'
import { formatLatency } from '../lib/format'
import { summarizeLatencyTargets } from '../lib/latencyTargets'
import { LatencyChart } from './LatencyChart'

interface LatencyDetailProps {
  node: HomeCardNode
  points: LatencyPoint[]
  range: string
  loading?: boolean
  error?: string
  onBack: () => void
  onRangeChange: (range: string) => void
}

const rangeOptions = [
  { value: '1d', label: '1 天' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
]

export function LatencyDetail({ node, points, range, loading = false, error, onBack, onRangeChange }: LatencyDetailProps) {
  const targetSummaries = useMemo(() => summarizeLatencyTargets(points), [points])
  const [activeTargetId, setActiveTargetId] = useState<string | null>(null)
  const activeTarget = targetSummaries.find((target) => target.targetId === activeTargetId) ?? targetSummaries[0]

  return (
    <div className="kulin-container detail-container">
      <button className="detail-title-button" type="button" onClick={onBack}>
        <BackIcon />
        <span>{node.displayName}</span>
      </button>

      <section className="detail-info-grid" aria-label={`${node.displayName} server facts`}>
        <InfoCard label="状态" value={node.status === 'online' ? '在线' : node.status} />
        <InfoCard label="架构" value="x86_64" />
        <InfoCard label="内存" value={formatBinaryBytes(node.memoryTotalBytes)} />
        <InfoCard label="磁盘" value={formatBinaryBytes(node.diskTotalBytes)} />
        <InfoCard label="区域" value={node.countryCode ?? '--'} />
      </section>

      <section className="detail-range-row" aria-label="latency range selector">
        {rangeOptions.map((option) => (
          <button
            key={option.value}
            type="button"
            className={range === option.value ? 'is-active' : ''}
            onClick={() => onRangeChange(option.value)}
          >
            {option.label}
          </button>
        ))}
        <label className="peak-switch">
          <input type="checkbox" aria-label="削峰" />
          <span />
          <b>削峰</b>
        </label>
      </section>

      <section className="monitor-panel" aria-label={`${node.displayName} network latency`}>
        <header className="monitor-heading">
          <div>
            <h3>{node.displayName}</h3>
            <p>{targetSummaries.length} 个监控服务</p>
          </div>
        </header>

        {loading && <div className="detail-state">正在读取网络延迟…</div>}
        {error && <div className="detail-state is-error">网络延迟读取失败：{error}</div>}

        {!loading && !error && (
          <>
            <div className="latency-target-grid" aria-label="monitor services">
              {targetSummaries.map((target) => (
                <button
                  key={target.targetId}
                  type="button"
                  data-active={target.targetId === activeTarget?.targetId}
                  onClick={() => setActiveTargetId(target.targetId)}
                >
                  <span>{target.targetName}</span>
                  <strong>{formatLatency(target.avgMs)}</strong>
                  <em>丢包 {formatLossPercent(target.lossPercent)}</em>
                </button>
              ))}
            </div>

            <LatencyChart
              points={points}
              title={`${activeTarget?.targetName ?? node.displayName} 网络延迟`}
              eyebrow={`${rangeOptions.find((option) => option.value === range)?.label ?? range} · ${targetSummaries.length} 个监控服务`}
              compactHeader
            />
          </>
        )}
      </section>
    </div>
  )
}

function InfoCard({ label, value }: { label: string; value: string }) {
  return (
    <article className="detail-info-card">
      <p>{label}</p>
      <strong>{value}</strong>
    </article>
  )
}

function formatBinaryBytes(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  if (value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  return `${size.toFixed(unit === 0 ? 0 : 2)} ${units[unit]}`
}

function formatLossPercent(value: number | null | undefined): string {
  if (value === null || value === undefined) return 'No data'
  return `${value.toFixed(2)}%`
}

function BackIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="m15 18-6-6 6-6" />
    </svg>
  )
}
