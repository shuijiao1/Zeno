import type { LatencyPoint } from '../types'

export interface LatencyTargetSummary {
  targetId: string
  targetName: string
  sampleCount: number
  avgMs: number | null
  lossPercent: number
}

interface Accumulator {
  targetId: string
  targetName: string
  latencyTotal: number
  latencyCount: number
  lossTotal: number
  lossCount: number
}

export function summarizeLatencyTargets(points: LatencyPoint[]): LatencyTargetSummary[] {
  const byTarget = new Map<string, Accumulator>()

  for (const point of points) {
    const existing = byTarget.get(point.targetId)
    const acc = existing ?? {
      targetId: point.targetId,
      targetName: point.targetName,
      latencyTotal: 0,
      latencyCount: 0,
      lossTotal: 0,
      lossCount: 0,
    }

    acc.targetName = point.targetName
    if (point.medianMs !== null && Number.isFinite(point.medianMs)) {
      acc.latencyTotal += point.medianMs
      acc.latencyCount += 1
    }
    if (Number.isFinite(point.lossPercent)) {
      acc.lossTotal += point.lossPercent
      acc.lossCount += 1
    }

    if (!existing) byTarget.set(point.targetId, acc)
  }

  return [...byTarget.values()].map((acc) => ({
    targetId: acc.targetId,
    targetName: acc.targetName,
    sampleCount: acc.latencyCount,
    avgMs: acc.latencyCount > 0 ? round2(acc.latencyTotal / acc.latencyCount) : null,
    lossPercent: acc.lossCount > 0 ? round2(acc.lossTotal / acc.lossCount) : 0,
  }))
}

function round2(value: number): number {
  return Math.round(value * 100) / 100
}
