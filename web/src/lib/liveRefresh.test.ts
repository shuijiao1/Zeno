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
})
