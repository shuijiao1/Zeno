import type { LiveWebSocketStatus } from '../api/client'

export type LiveDataSource = 'http' | 'ws'

export const initialLiveFallbackDelayMs = 1_800
export const sustainedLiveFallbackDelayMs = 90_000
export const httpFallbackIntervalMs = 15_000
export const httpFallbackTimeoutMs = 10_000

type LiveSubscribe<T> = (
  onData: (data: T) => void,
  onError?: (error: Error) => void,
  onStatus?: (status: LiveWebSocketStatus) => void,
) => (() => void) | null

export interface ResilientLiveOptions<T> {
  subscribe?: LiveSubscribe<T> | null
  fetch: (signal?: AbortSignal) => Promise<T>
  applyData: (data: T, source: LiveDataSource) => void
  onError?: (error: unknown, source: LiveDataSource) => void
  initialFallbackDelayMs?: number
  sustainedFallbackDelayMs?: number
  httpFallbackIntervalMs?: number
  httpFallbackTimeoutMs?: number
}

export function startResilientLiveData<T>(options: ResilientLiveOptions<T>): () => void {
  const initialDelayMs = options.initialFallbackDelayMs ?? initialLiveFallbackDelayMs
  const sustainedDelayMs = options.sustainedFallbackDelayMs ?? sustainedLiveFallbackDelayMs
  const intervalMs = options.httpFallbackIntervalMs ?? httpFallbackIntervalMs
  const timeoutMs = options.httpFallbackTimeoutMs ?? httpFallbackTimeoutMs
  let stopped = false
  let receivedLiveFrame = false
  let fallbackActive = false
  let fallbackInFlight = false
  let fallbackGeneration = 0
  let initialFallbackTimer: ReturnType<typeof setTimeout> | null = null
  let sustainedFallbackTimer: ReturnType<typeof setTimeout> | null = null
  let fallbackInterval: ReturnType<typeof setInterval> | null = null
  let fallbackController: AbortController | null = null
  let fallbackRequestId = 0
  let activeFallbackRequestId = 0
  let fallbackTimeout: ReturnType<typeof setTimeout> | null = null
  let stopStream: (() => void) | null = null

  const clearInitialTimer = () => {
    if (initialFallbackTimer !== null) {
      clearTimeout(initialFallbackTimer)
      initialFallbackTimer = null
    }
  }
  const clearSustainedTimer = () => {
    if (sustainedFallbackTimer !== null) {
      clearTimeout(sustainedFallbackTimer)
      sustainedFallbackTimer = null
    }
  }
  const clearFallbackInterval = () => {
    if (fallbackInterval !== null) {
      clearInterval(fallbackInterval)
      fallbackInterval = null
    }
  }

  const cancelFallbackRequest = () => {
    if (fallbackTimeout !== null) {
      clearTimeout(fallbackTimeout)
      fallbackTimeout = null
    }
    fallbackController?.abort()
    fallbackController = null
  }

  const finishFallbackRequest = (controller: AbortController, requestId: number) => {
    if (fallbackController !== controller || activeFallbackRequestId !== requestId) return
    if (fallbackTimeout !== null) clearTimeout(fallbackTimeout)
    fallbackTimeout = null
    fallbackController = null
    fallbackInFlight = false
  }

  const runHttpFallbackOnce = () => {
    if (stopped || fallbackInFlight) return
    fallbackInFlight = true
    const generation = fallbackGeneration
    const controller = new AbortController()
    const requestId = fallbackRequestId + 1
    fallbackRequestId = requestId
    activeFallbackRequestId = requestId
    fallbackController = controller
    fallbackTimeout = setTimeout(() => {
      controller.abort()
      finishFallbackRequest(controller, requestId)
    }, timeoutMs)
    options.fetch(controller.signal)
      .then((data) => {
        if (!stopped && fallbackActive && generation === fallbackGeneration && fallbackController === controller && activeFallbackRequestId === requestId && !controller.signal.aborted) options.applyData(data, 'http')
      })
      .catch((error: unknown) => {
        if (!stopped && fallbackController === controller && activeFallbackRequestId === requestId && !controller.signal.aborted) options.onError?.(error, 'http')
      })
      .finally(() => {
        finishFallbackRequest(controller, requestId)
      })
  }

  const startFallback = () => {
    if (stopped || fallbackActive) return
    fallbackActive = true
    fallbackGeneration += 1
    clearInitialTimer()
    clearSustainedTimer()
    runHttpFallbackOnce()
    fallbackInterval = setInterval(runHttpFallbackOnce, intervalMs)
  }

  const stopFallback = () => {
    if (!fallbackActive) return
    fallbackActive = false
    fallbackGeneration += 1
    clearFallbackInterval()
    cancelFallbackRequest()
    fallbackInFlight = false
  }

  const scheduleInitialFallback = () => {
    if (receivedLiveFrame || initialFallbackTimer !== null || fallbackActive) return
    initialFallbackTimer = setTimeout(() => {
      initialFallbackTimer = null
      if (!receivedLiveFrame) startFallback()
    }, initialDelayMs)
  }

  const scheduleSustainedFallback = () => {
    if (!receivedLiveFrame || sustainedFallbackTimer !== null || fallbackActive) return
    sustainedFallbackTimer = setTimeout(() => {
      sustainedFallbackTimer = null
      startFallback()
    }, sustainedDelayMs)
  }

  if (!options.subscribe) {
    startFallback()
    return () => {
      stopped = true
      clearInitialTimer()
      clearSustainedTimer()
      clearFallbackInterval()
      cancelFallbackRequest()
    }
  }

  stopStream = options.subscribe(
    (data) => {
      if (stopped) return
      receivedLiveFrame = true
      clearInitialTimer()
      clearSustainedTimer()
      stopFallback()
      options.applyData(data, 'ws')
      // Keep a frame-freshness watchdog even while the socket still reports
      // open. Half-open connections do not always emit close/error promptly.
      scheduleSustainedFallback()
    },
    (error) => {
      if (stopped) return
      options.onError?.(error, 'ws')
      if (receivedLiveFrame) scheduleSustainedFallback()
    },
    (status) => {
      if (stopped) return
      if (status === 'open') {
        // A successful handshake does not prove the stream is healthy. Keep an
        // active fallback (or its sustained-outage timer) until a fresh WS frame
        // actually arrives; otherwise a half-open socket can freeze the page.
        if (!receivedLiveFrame) scheduleInitialFallback()
        return
      }
      if (status === 'reconnecting' || status === 'closed') {
        if (receivedLiveFrame) scheduleSustainedFallback()
        else scheduleInitialFallback()
      }
    },
  )

  if (!stopStream) startFallback()
  else scheduleInitialFallback()

  return () => {
    stopped = true
    clearInitialTimer()
    clearSustainedTimer()
    clearFallbackInterval()
    cancelFallbackRequest()
    stopStream?.()
  }
}
