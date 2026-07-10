export const historyRangeOptions = [
  { value: '1h', label: '实时' },
  { value: '1d', label: '1 天' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
]

export function rangeRequiresAdmin(range: string): boolean {
  return range === '7d' || range === '30d'
}

export function availableHistoryRanges(hasAdminToken: boolean) {
  return hasAdminToken ? historyRangeOptions : historyRangeOptions.filter((option) => !rangeRequiresAdmin(option.value))
}

export function coerceHistoryRange(range: string, hasAdminToken: boolean, fallback = '1d'): string {
  if (availableHistoryRanges(hasAdminToken).some((option) => option.value === range)) return range
  return fallback
}

export function isHTTPUnauthorizedError(error: unknown): boolean {
  return error instanceof Error && /(?:request failed|failed): 401$/.test(error.message)
}
