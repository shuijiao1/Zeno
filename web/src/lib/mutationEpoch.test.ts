import { describe, expect, it } from 'vitest'

import { createMutationEpoch } from './mutationEpoch'

describe('mutation epoch', () => {
  it('invalidates a refresh that started before a mutation', () => {
    const epoch = createMutationEpoch()
    const staleRefresh = epoch.snapshot()

    epoch.invalidate()

    expect(epoch.isCurrent(staleRefresh)).toBe(false)
    expect(epoch.isCurrent(epoch.snapshot())).toBe(true)
  })

  it('invalidates each older mutation window independently', () => {
    const epoch = createMutationEpoch()
    const first = epoch.invalidate()
    const second = epoch.invalidate()

    expect(epoch.isCurrent(first)).toBe(false)
    expect(epoch.isCurrent(second)).toBe(true)
  })

  it('rejects refreshes launched while a mutation is pending', () => {
    const epoch = createMutationEpoch()
    const finishMutation = epoch.beginMutation()
    const duringMutationRefresh = epoch.snapshot()

    expect(epoch.isCurrent(duringMutationRefresh)).toBe(false)

    finishMutation()

    expect(epoch.isCurrent(duringMutationRefresh)).toBe(false)
    expect(epoch.isCurrent(epoch.snapshot())).toBe(true)
  })

  it('keeps concurrent mutation windows stale until all mutations finish', () => {
    const epoch = createMutationEpoch()
    const finishFirst = epoch.beginMutation()
    const finishSecond = epoch.beginMutation()
    const betweenMutations = epoch.snapshot()

    finishFirst()

    expect(epoch.isCurrent(betweenMutations)).toBe(false)

    finishSecond()

    expect(epoch.isCurrent(betweenMutations)).toBe(false)
    expect(epoch.isCurrent(epoch.snapshot())).toBe(true)
  })
})
