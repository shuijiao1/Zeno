export const liveRefreshIntervalMs = 15_000
export const liveRefreshTimeoutMs = 10_000

type MaybePromise<T = void> = T | Promise<T>

export interface LiveRefreshOptions {
  intervalMs?: number
  timeoutMs?: number
  immediate?: boolean
}

function isPromiseLike(value: unknown): value is PromiseLike<unknown> {
  return typeof value === 'object' && value !== null && 'then' in value && typeof (value as { then?: unknown }).then === 'function'
}

export function startLiveRefresh(refresh: (signal?: AbortSignal) => MaybePromise, intervalOrOptions: number | LiveRefreshOptions = liveRefreshIntervalMs): () => void {
  const options: LiveRefreshOptions = typeof intervalOrOptions === 'number' ? { intervalMs: intervalOrOptions } : intervalOrOptions
  const intervalMs = options.intervalMs ?? liveRefreshIntervalMs
  const timeoutMs = options.timeoutMs ?? liveRefreshTimeoutMs
  let stopped = false
  let inFlight: AbortController | null = null
  let timeout: ReturnType<typeof setTimeout> | null = null

  const clearTimeoutHandle = () => {
    if (timeout !== null) {
      clearTimeout(timeout)
      timeout = null
    }
  }

  const finish = (controller: AbortController) => {
    if (inFlight !== controller) return
    clearTimeoutHandle()
    inFlight = null
  }

  const run = () => {
    if (stopped || inFlight) return
    const controller = new AbortController()
    inFlight = controller
    timeout = setTimeout(() => {
      controller.abort()
      finish(controller)
    }, timeoutMs)
    try {
      const result = refresh(controller.signal)
      if (isPromiseLike(result)) {
        Promise.resolve(result).catch(() => {}).finally(() => finish(controller))
      } else {
        finish(controller)
      }
    } catch {
      finish(controller)
    }
  }

  if (options.immediate) run()
  const timer = globalThis.setInterval(run, intervalMs)
  return () => {
    stopped = true
    globalThis.clearInterval(timer)
    clearTimeoutHandle()
    inFlight?.abort()
    inFlight = null
  }
}
