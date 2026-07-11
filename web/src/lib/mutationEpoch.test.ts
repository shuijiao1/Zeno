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
})
