import { formatBytes, formatPercent } from '../lib/format'

interface ResourceBarProps {
  label: string
  percent: number | null
  usedBytes?: number | null
  totalBytes?: number | null
  valueText?: string
}

function levelClass(percent: number | null): string {
  if (percent === null || Number.isNaN(percent)) return 'is-empty'
  if (percent >= 85) return 'is-danger'
  if (percent >= 70) return 'is-warning'
  return 'is-good'
}

export function ResourceBar({ label, percent, usedBytes, totalBytes, valueText }: ResourceBarProps) {
  const safePercent = percent === null || Number.isNaN(percent) ? 0 : Math.max(0, Math.min(100, percent))
  const resolvedValue = valueText ?? (
    usedBytes !== undefined || totalBytes !== undefined
      ? `${formatBytes(usedBytes)} / ${formatBytes(totalBytes)}`
      : formatPercent(percent)
  )

  return (
    <div className="resource-row">
      <div className="resource-row__meta">
        <span>{label}</span>
        <strong>{resolvedValue}</strong>
      </div>
      <div className="resource-row__track" aria-label={`${label} ${formatPercent(percent)}`}>
        <div className={`resource-row__fill ${levelClass(percent)}`} style={{ width: `${safePercent}%` }} />
      </div>
    </div>
  )
}
