export const liveRefreshIntervalMs = 60_000

export function startLiveRefresh(refresh: () => void, intervalMs = liveRefreshIntervalMs): () => void {
  const timer = globalThis.setInterval(refresh, intervalMs)
  return () => globalThis.clearInterval(timer)
}
