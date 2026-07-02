import type { LatencyPoint } from '../types'

export interface LatencySeries {
  targetId: string
  targetName: string
  strokeWidth: 1
  color: string
  points: LatencyPoint[]
}

const palette = ['#22c55e', '#38bdf8', '#f59e0b', '#a78bfa', '#fb7185', '#14b8a6']

export function buildLatencySeries(points: LatencyPoint[]): LatencySeries[] {
  const order: string[] = []
  const byTarget = new Map<string, LatencyPoint[]>()

  for (const point of points) {
    if (!byTarget.has(point.targetId)) {
      byTarget.set(point.targetId, [])
      order.push(point.targetId)
    }
    byTarget.get(point.targetId)!.push(point)
  }

  return order.map((targetId, index) => {
    const targetPoints = [...byTarget.get(targetId)!].sort((a, b) => a.ts.localeCompare(b.ts))
    return {
      targetId,
      targetName: targetPoints[0]?.targetName ?? targetId,
      strokeWidth: 1 as const,
      color: palette[index % palette.length],
      points: targetPoints,
    }
  })
}

export function yDomain(points: LatencyPoint[]): { min: number; max: number } {
  const values = points
    .map((point) => point.medianMs)
    .filter((value): value is number => value !== null && value !== undefined)
  if (values.length === 0) {
    return { min: 0, max: 1 }
  }
  const max = Math.max(...values)
  return { min: 0, max: Math.max(1, Math.ceil(max * 1.2)) }
}
