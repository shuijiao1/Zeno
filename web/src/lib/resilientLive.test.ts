import { afterEach, describe, expect, it, vi } from 'vitest'
import { startResilientLiveData } from './resilientLive'
import type { LiveWebSocketStatus } from '../api/client'

afterEach(() => {
  vi.useRealTimers()
})

describe('startResilientLiveData', () => {
  it('starts sustained HTTP fallback after 90s of websocket reconnecting and applies HTTP after prior WS frames', async () => {
    vi.useFakeTimers()
    let pushLive = (_data: string) => {}
    let pushStatus = (_status: LiveWebSocketStatus) => {}
    const fetch = vi.fn().mockResolvedValue('http-after-drop')
    const applyData = vi.fn()

    const stop = startResilientLiveData<string>({
      subscribe: (onData, _onError, onStatus) => {
        pushLive = onData
        pushStatus = onStatus ?? pushStatus
        onStatus?.('open')
        return vi.fn()
      },
      fetch,
      applyData,
    })

    pushLive('ws-frame')
    expect(applyData).toHaveBeenLastCalledWith('ws-frame', 'ws')

    pushStatus('reconnecting')
    await vi.advanceTimersByTimeAsync(89_999)
    expect(fetch).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(1)
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(applyData).toHaveBeenLastCalledWith('http-after-drop', 'http')

    stop()
  })

  it('aborts a stalled HTTP fallback and allows the next interval to retry', async () => {
    vi.useFakeTimers()
    const signals: AbortSignal[] = []
    const fetch = vi.fn((signal?: AbortSignal) => {
      if (signal) signals.push(signal)
      return new Promise<string>((_resolve, reject) => {
        signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true })
      })
    })

    const stop = startResilientLiveData<string>({
      subscribe: null,
      fetch,
      applyData: vi.fn(),
      httpFallbackTimeoutMs: 10_000,
      httpFallbackIntervalMs: 15_000,
    })

    expect(fetch).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(signals[0]?.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(5_000)
    expect(fetch).toHaveBeenCalledTimes(2)

    stop()
  })

  it('keeps HTTP fallback through a bare websocket handshake and stops only after a fresh frame', async () => {
    vi.useFakeTimers()
    let pushLive = (_data: string) => {}
    let pushStatus = (_status: LiveWebSocketStatus) => {}
    const fetch = vi.fn().mockResolvedValue('http-fallback')
    const applyData = vi.fn()

    const stop = startResilientLiveData<string>({
      subscribe: (onData, _onError, onStatus) => {
        pushLive = onData
        pushStatus = onStatus ?? pushStatus
        onStatus?.('open')
        return vi.fn()
      },
      fetch,
      applyData,
    })

    pushLive('ws-frame')
    pushStatus('reconnecting')
    await vi.advanceTimersByTimeAsync(90_000)
    expect(fetch).toHaveBeenCalledTimes(1)

    pushStatus('open')
    await vi.advanceTimersByTimeAsync(15_000)
    expect(fetch).toHaveBeenCalledTimes(2)

    pushLive('ws-recovered')
    expect(applyData).toHaveBeenLastCalledWith('ws-recovered', 'ws')
    await vi.advanceTimersByTimeAsync(45_000)
    expect(fetch).toHaveBeenCalledTimes(2)

    stop()
  })

  it('does not let a stale aborted request unlock a newer fallback request', async () => {
    vi.useFakeTimers()
    let pushLive = (_data: string) => {}
    let pushStatus = (_status: LiveWebSocketStatus) => {}
    const resolvers: Array<(value: string) => void> = []
    const fetch = vi.fn(() => new Promise<string>((resolve) => resolvers.push(resolve)))

    const stop = startResilientLiveData<string>({
      subscribe: (onData, _onError, onStatus) => {
        pushLive = onData
        pushStatus = onStatus ?? pushStatus
        onStatus?.('open')
        return vi.fn()
      },
      fetch,
      applyData: vi.fn(),
    })

    await vi.advanceTimersByTimeAsync(1_800)
    expect(fetch).toHaveBeenCalledTimes(1)
    pushLive('ws-frame')
    pushStatus('reconnecting')
    await vi.advanceTimersByTimeAsync(90_000)
    expect(fetch).toHaveBeenCalledTimes(2)

    resolvers[0]?.('stale-http')
    await Promise.resolve()
    await vi.advanceTimersByTimeAsync(15_000)
    expect(fetch).toHaveBeenCalledTimes(2)

    stop()
  })

  it('starts HTTP fallback when an open websocket silently stops producing frames', async () => {
    vi.useFakeTimers()
    let pushLive = (_data: string) => {}
    const fetch = vi.fn().mockResolvedValue('http-after-stall')
    const applyData = vi.fn()
    const stop = startResilientLiveData<string>({
      subscribe: (onData, _onError, onStatus) => {
        pushLive = onData
        onStatus?.('open')
        return vi.fn()
      },
      fetch,
      applyData,
    })

    pushLive('initial-frame')
    await vi.advanceTimersByTimeAsync(89_999)
    expect(fetch).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(applyData).toHaveBeenLastCalledWith('http-after-stall', 'http')

    stop()
  })
})
