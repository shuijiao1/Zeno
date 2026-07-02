import { useMemo, useState } from 'react'
import type { HomeCardNode, LatencyPoint, StatePoint } from '../types'
import { formatLatency } from '../lib/format'
import { summarizeLatencyTargets } from '../lib/latencyTargets'
import { LatencyChart } from './LatencyChart'
import { StateHistoryPanel } from './StateHistoryPanel'

interface LatencyDetailProps {
  node: HomeCardNode
  points: LatencyPoint[]
  statePoints?: StatePoint[]
  range: string
  loading?: boolean
  error?: string
  stateLoading?: boolean
  stateError?: string
  onBack: () => void
  onRangeChange: (range: string) => void
}

const rangeOptions = [
  { value: '1d', label: '1 天' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
]

export function LatencyDetail({
  node,
  points,
  statePoints = [],
  range,
  loading = false,
  error,
  stateLoading = false,
  stateError,
  onBack,
  onRangeChange,
}: LatencyDetailProps) {
  const targetSummaries = useMemo(() => summarizeLatencyTargets(points), [points])
  const [activeTargetIds, setActiveTargetIds] = useState<string[]>([])
  const [peakCut, setPeakCut] = useState(false)
  const activeTargetNames = targetSummaries
    .filter((target) => activeTargetIds.includes(target.targetId))
    .map((target) => target.targetName)
  const rangeLabel = rangeOptions.find((option) => option.value === range)?.label ?? range
  const toggleTarget = (targetId: string) => {
    setActiveTargetIds((current) => (
      current.includes(targetId) ? current.filter((id) => id !== targetId) : [...current, targetId]
    ))
  }

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
          <input type="checkbox" aria-label="削峰" checked={peakCut} onChange={(event) => setPeakCut(event.target.checked)} />
          <span />
          <b>削峰</b>
        </label>
      </section>

      <StateHistoryPanel
        points={statePoints}
        rangeLabel={rangeLabel}
        loading={stateLoading}
        error={stateError}
      />

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
                  data-active={activeTargetIds.includes(target.targetId)}
                  onClick={() => toggleTarget(target.targetId)}
                >
                  <span>{target.targetName}</span>
                  <strong>{formatLatency(target.avgMs)}</strong>
                  <em>丢包 {formatLossPercent(target.lossPercent)}</em>
                </button>
              ))}
            </div>

            <LatencyChart
              points={points}
              title={`${node.displayName} 网络延迟`}
              eyebrow={`${rangeLabel} · ${targetSummaries.length} 个监控服务${peakCut ? ' · 削峰' : ''}`}
              compactHeader
              peakCut={peakCut}
              activeTargetNames={activeTargetNames}
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
