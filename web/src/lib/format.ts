export function formatBytes(value: number | null | undefined): string {
  if (value === null || value === undefined) return 'No data'
  if (value === 0) return '0 B'

  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let size = Math.abs(value)
  let unitIndex = 0
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex += 1
  }

  const signed = value < 0 ? -size : size
  if (unitIndex === 0) return `${Math.round(signed)} ${units[unitIndex]}`
  return `${signed.toFixed(1)} ${units[unitIndex]}`
}

export function formatBps(value: number | null | undefined): string {
  const formatted = formatBytes(value)
  return formatted === 'No data' ? formatted : `${formatted}/s`
}

export function formatPercent(value: number | null | undefined): string {
  if (value === null || value === undefined) return 'No data'
  return `${value.toFixed(1)}%`
}

export function formatLatency(value: number | null | undefined): string {
  if (value === null || value === undefined) return 'No data'
  return `${value.toFixed(value >= 100 ? 0 : 1)} ms`
}
