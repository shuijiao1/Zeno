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
      <section className="detail-hero" aria-label={`${node.displayName} server overview`}>
        <div className="detail-hero__main">
          <button className="detail-title-button" type="button" onClick={onBack}>
            <BackIcon />
            <span>{node.displayName}</span>
          </button>
          <span className={`detail-status-pill status-${node.status}`}>{formatStatusLabel(node.status)}</span>
        </div>
        <section className="detail-fact-strip" aria-label={`${node.displayName} server facts`}>
          <InfoFact label="系统" value={`${formatOSLabel(node)} · ${node.arch || '--'}`} />
          <InfoFact label="CPU" value={node.cpuModel || '--'} wide />
          <InfoFact label="规格" value={`${formatCores(node.cpuCores)} · ${node.virtualization || '--'}`} />
          <InfoFact label="内核" value={node.kernel || '--'} />
          <InfoFact label="内存 / 磁盘" value={`${formatBinaryBytes(node.memoryTotalBytes)} / ${formatBinaryBytes(node.diskTotalBytes)}`} />
          <InfoFact label="区域" value={node.countryCode ?? '--'} />
        </section>
      </section>

      <section className="monitor-panel" aria-label={`${node.displayName} network latency`}>
        <header className="monitor-heading">
          <div>
            <h3>{node.displayName}</h3>
            <p>{targetSummaries.length} 个监控服务</p>
          </div>
          <div className="monitor-heading-actions">
            <div className="detail-range-row" aria-label="latency range selector">
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
            </div>
            <label className="peak-switch">
              <input type="checkbox" aria-label="削峰" checked={peakCut} onChange={(event) => setPeakCut(event.target.checked)} />
              <span />
              <b>削峰</b>
            </label>
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

      <StateHistoryPanel
        points={statePoints}
        rangeLabel={rangeLabel}
        loading={stateLoading}
        error={stateError}
      />
    </div>
  )
}

function InfoFact({ label, value, wide = false }: { label: string; value: string; wide?: boolean }) {
  return (
    <article className={`detail-fact${wide ? ' is-wide' : ''}`} title={`${label}: ${value}`}>
      <p>{label}</p>
      <strong>{value}</strong>
    </article>
  )
}

function formatStatusLabel(status: HomeCardNode['status']): string {
  if (status === 'online') return '在线'
  if (status === 'offline') return '离线'
  if (status === 'warning') return '异常'
  if (status === 'no_data') return '暂无数据'
  return status
}

function formatOSLabel(node: HomeCardNode): string {
  return [node.os, node.osVersion].filter(Boolean).join(' ') || '--'
}

function formatCores(value: number | null | undefined): string {
  if (value === null || value === undefined) return '-- Cores'
  return `${Number.isInteger(value) ? value.toFixed(0) : value.toFixed(1)} Cores`
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
