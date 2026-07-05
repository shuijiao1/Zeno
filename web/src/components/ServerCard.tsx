import type { ReactNode } from 'react'
import type { HomeCardNode } from '../types'
import { formatLatency } from '../lib/format'

interface ServerCardProps {
  node: HomeCardNode
  onOpen?: (nodeId: string) => void
}

const osAsset: Record<HomeCardNode['os'], string> = {
  debian: '/assets/logo/os-debian.svg',
  ubuntu: '/assets/logo/os-ubuntu.svg',
  windows: '/assets/logo/os-windows.svg',
  centos: '/assets/logo/linux.svg',
  alpine: '/assets/logo/linux.svg',
  linux: '/assets/logo/linux.svg',
  unknown: '/assets/logo/linux.svg',
}

function flag(countryCode?: string): string {
  if (!countryCode || countryCode.length !== 2) return '🏳️'
  const base = 127397
  return countryCode
    .toUpperCase()
    .split('')
    .map((char) => String.fromCodePoint(char.charCodeAt(0) + base))
    .join('')
}

function ratio(used: number | null | undefined, total: number | null | undefined): number | null {
  if (used === null || used === undefined || total === null || total === undefined || total <= 0) return null
  return (used / total) * 100
}

function clampPercent(value: number | null | undefined): number {
  if (value === null || value === undefined || !Number.isFinite(value)) return 0
  return Math.max(0, Math.min(100, value))
}

function barTone(value: number | null | undefined): 'good' | 'warning' | 'danger' | 'empty' {
  if (value === null || value === undefined) return 'empty'
  if (value > 90) return 'danger'
  if (value > 60) return 'warning'
  return 'good'
}

function formatKulinBytes(value: number | null | undefined, options: { compact?: boolean } = {}): string {
  if (value === null || value === undefined) return '--'
  if (value === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let size = Math.abs(value)
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  const signed = value < 0 ? -size : size
  const digits = unit === 0 ? 0 : 2
  const joiner = options.compact ? '' : ' '
  return `${signed.toFixed(digits)}${joiner}${units[unit]}`
}

function formatRate(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  return `${formatKulinBytes(value)}/s`
}

function formatCores(value: number | null | undefined): string {
  if (value === null || value === undefined) return '-- Cores'
  return `${value.toFixed(value % 1 === 0 ? 0 : 1)} ${value === 1 ? 'Core' : 'Cores'}`
}

function formatUsage(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  return value.toFixed(2)
}

function normalizeLoss(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  return `${value.toFixed(2)}%`
}

function formatTrafficLabel(node: HomeCardNode): string {
  const range = formatBillingPeriodRange(node.monthlyPeriodStart, node.monthlyPeriodEnd)
  if (range) return `流量 · ${range}`
  if (node.monthlyResetDay) return `流量 · 每月 ${node.monthlyResetDay} 日重置`
  return '流量'
}

function formatBillingPeriodRange(start?: string, end?: string): string {
  if (!start || !end) return ''
  return `${formatMonthDay(start)}–${formatMonthDay(end)}`
}

function formatMonthDay(value: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!match) return value
  return `${Number(match[2])}/${Number(match[3])}`
}

export function ServerCard({ node, onOpen }: ServerCardProps) {
  const memoryPercent = ratio(node.memoryUsedBytes, node.memoryTotalBytes)
  const diskPercent = ratio(node.diskUsedBytes, node.diskTotalBytes)
  const trafficPercent = ratio(node.monthlyBillableBytes, node.monthlyQuotaBytes)
  const latency = node.latencySummary
  const open = () => onOpen?.(node.id)
  const isOfflineCard = node.status === 'offline' || node.status === 'no_data'

  return (
    <article
      className={`kulin-node-card${isOfflineCard ? ' is-offline' : ''}`}
      role={onOpen ? 'link' : undefined}
      tabIndex={onOpen ? 0 : undefined}
      onClick={open}
      onKeyDown={(event) => {
        if (!onOpen) return
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault()
          open()
        }
      }}
    >
      {isOfflineCard && (
        <div className="node-offline-state" aria-label={`${node.displayName} 离线`}>
          <span className="node-dot status-offline" aria-hidden="true" />
          <span>离线</span>
        </div>
      )}
      {!isOfflineCard && (
        <>
      <section className="node-head">
        <img alt={node.os} className="node-os" loading="lazy" src={osAsset[node.os]} />
        <div className="node-title-line">
          <span className={`node-dot status-${node.status}`} />
          <span className="node-flag">{flag(node.countryCode)}</span>
          <p>{node.displayName}</p>
        </div>
        <div className="node-expiry">{node.expiryLabel ?? '永 久'}</div>
      </section>

      <section className="node-specs" aria-label={`${node.displayName} specs`}>
        <SpecIcon kind="cpu" label={formatCores(node.cpuCores)} />
        <SpecIcon kind="memory" label={formatKulinBytes(node.memoryTotalBytes)} />
        <SpecIcon kind="disk" label={formatKulinBytes(node.diskTotalBytes)} />
      </section>

      <section className="node-usage" aria-label={`${node.displayName} usage`}>
        <UsageBar label="CPU" valueText={`${formatUsage(node.cpuPercent)}%`} percent={node.cpuPercent} />
        <div className="node-usage-rest">
          <UsageBar label="内存" valueText={`${formatUsage(memoryPercent)}%`} percent={memoryPercent} />
          <UsageBar label="存储" valueText={`${formatUsage(diskPercent)}%`} percent={diskPercent} />
          <UsageBar label={formatTrafficLabel(node)} valueText={`${formatKulinBytes(node.monthlyBillableBytes, { compact: true })} / ${formatKulinBytes(node.monthlyQuotaBytes, { compact: true })}`} percent={trafficPercent} />
          <section className="node-footer-grid" aria-label={`${node.displayName} network and latency`}>
            <Metric tone="up" icon={<UploadIcon />} label="上传" value={formatRate(node.netOutSpeedBps)} />
            <Metric tone="down" icon={<DownloadIcon />} label="下载" value={formatRate(node.netInSpeedBps)} />
            {latency && <Metric tone="latency" icon={<ActivityIcon />} label="延迟" value={formatLatency(latency.medianMs)} />}
            {latency && <Metric tone="loss" icon={<TriangleAlertIcon />} label="丢包率" value={normalizeLoss(latency.lossPercent)} />}
          </section>
        </div>
      </section>
        </>
      )}
    </article>
  )
}

function UsageBar({ label, valueText, percent }: { label: string; valueText: string; percent: number | null | undefined }) {
  const value = clampPercent(percent)
  return (
    <div className="usage-row">
      <div className="usage-row__meta">
        <span>{label}</span>
        <strong>{valueText}</strong>
      </div>
      <div className="usage-track" role="progressbar" aria-label="Server Usage Bar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={value}>
        <div className={`usage-fill is-${barTone(percent)}`} style={{ transform: `translateX(-${100 - value}%)` }} />
      </div>
    </div>
  )
}

function Metric({ tone, icon, label, value }: { tone: 'up' | 'down' | 'latency' | 'loss'; icon: ReactNode; label: string; value: string }) {
  return (
    <div className={`node-metric metric-${tone}`}>
      <span className="metric-icon">{icon}</span>
      <span className="metric-label">{label}</span>
      <strong>{value}</strong>
    </div>
  )
}

function SpecIcon({ kind, label }: { kind: 'cpu' | 'memory' | 'disk'; label: string }) {
  return (
    <div className={`node-spec spec-${kind}`}>
      <svg viewBox="0 0 24 24" aria-hidden="true">
        {kind === 'cpu' && <><rect x="5" y="5" width="14" height="14" rx="2" /><rect x="9" y="9" width="6" height="6" rx="1" /><path d="M9 1v3M15 1v3M9 20v3M15 20v3M1 9h3M1 15h3M20 9h3M20 15h3" /></>}
        {kind === 'memory' && <><rect x="3" y="7" width="18" height="10" rx="2" /><path d="M7 11v2M11 11v2M15 11v2M19 11v2M5 17v3M9 17v3M15 17v3M19 17v3" /></>}
        {kind === 'disk' && <><path d="M5 5h14l3 7v5a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-5l3-7Z" /><path d="M3 12h18" /><circle cx="7" cy="16" r="1" /><circle cx="11" cy="16" r="1" /></>}
      </svg>
      <span>{label}</span>
    </div>
  )
}

function UploadIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 3v12" />
      <path d="m17 8-5-5-5 5" />
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
    </svg>
  )
}

function DownloadIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 15V3" />
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
      <path d="m7 10 5 5 5-5" />
    </svg>
  )
}

function ActivityIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2" />
    </svg>
  )
}

function TriangleAlertIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3" />
      <path d="M12 9v4" />
      <path d="M12 17h.01" />
    </svg>
  )
}
