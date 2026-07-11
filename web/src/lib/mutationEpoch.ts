export interface MutationEpoch {
  snapshot: () => number
  invalidate: () => number
  isCurrent: (snapshot: number) => boolean
}

export function createMutationEpoch(): MutationEpoch {
  let value = 0
  return {
    snapshot: () => value,
    invalidate: () => {
      value += 1
      return value
    },
    isCurrent: (snapshot) => snapshot === value,
  }
}
