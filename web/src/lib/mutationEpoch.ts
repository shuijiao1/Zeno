export interface MutationEpoch {
  snapshot: () => number
  invalidate: () => number
  beginMutation: () => () => void
  isCurrent: (snapshot: number) => boolean
}

export function createMutationEpoch(): MutationEpoch {
  let value = 0
  let pendingMutations = 0
  const bump = () => {
    value += 1
    return value
  }
  return {
    snapshot: () => value,
    invalidate: bump,
    beginMutation: () => {
      pendingMutations += 1
      bump()
      let finished = false
      return () => {
        if (finished) return
        finished = true
        pendingMutations = Math.max(0, pendingMutations - 1)
        bump()
      }
    },
    isCurrent: (snapshot) => pendingMutations === 0 && snapshot === value,
  }
}
