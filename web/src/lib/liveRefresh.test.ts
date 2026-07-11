import { afterEach, describe, expect, it, vi } from 'vitest'
import { startLiveRefresh } from './liveRefresh'

afterEach(() => {
  vi.useRealTimers()
})

describe('startLiveRefresh', () => {
  it('runs a callback on a fixed interval until stopped', () => {
    vi.useFakeTimers()
    const refresh = vi.fn()

    const stop = startLiveRefresh(refresh, 1000)

    expect(refresh).not.toHaveBeenCalled()
    vi.advanceTimersByTime(3000)
    expect(refresh).toHaveBeenCalledTimes(3)

    stop()
    vi.advanceTimersByTime(3000)
    expect(refresh).toHaveBeenCalledTimes(3)
  })

  it('uses a 15 second default interval for live monitoring pages', () => {
    vi.useFakeTimers()
    const refresh = vi.fn()

    const stop = startLiveRefresh(refresh)

    vi.advanceTimersByTime(14_000)
    expect(refresh).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1_000)
    expect(refresh).toHaveBeenCalledTimes(1)

    stop()
  })

  it('runs refreshes serially without overlapping slow requests', async () => {
    vi.useFakeTimers()
    let finishRefresh = () => {}
    const refresh = vi.fn(() => new Promise<void>((resolve) => {
      finishRefresh = resolve
    }))

    const stop = startLiveRefresh(refresh, { intervalMs: 1000, timeoutMs: 10_000 })

    await vi.advanceTimersByTimeAsync(5000)
    expect(refresh).toHaveBeenCalledTimes(1)

    finishRefresh()
    await vi.advanceTimersByTimeAsync(1000)
    expect(refresh).toHaveBeenCalledTimes(2)

    stop()
  })

  it('aborts an in-flight refresh when stopped', () => {
    vi.useFakeTimers()
    let signal: AbortSignal | undefined
    const refresh = vi.fn((nextSignal?: AbortSignal) => {
      signal = nextSignal
      return new Promise<void>(() => {})
    })

    const stop = startLiveRefresh(refresh, { intervalMs: 1000, immediate: true, timeoutMs: 10_000 })

    expect(refresh).toHaveBeenCalledTimes(1)
    expect(signal?.aborted).toBe(false)
    stop()
    expect(signal?.aborted).toBe(true)
  })

  it('times out a stuck refresh and allows the next interval to run', async () => {
    vi.useFakeTimers()
    const signals: AbortSignal[] = []
    const refresh = vi.fn((signal?: AbortSignal) => {
      if (signal) signals.push(signal)
      return new Promise<void>(() => {})
    })

    const stop = startLiveRefresh(refresh, { intervalMs: 1000, immediate: true, timeoutMs: 500 })

    expect(refresh).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(500)
    expect(signals[0].aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(500)
    expect(refresh).toHaveBeenCalledTimes(2)

    stop()
  })
})
