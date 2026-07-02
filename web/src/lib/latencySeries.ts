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

interface PeakCutOptions {
  peakCut?: boolean
  minSamples?: number
}

export function peakCutLatencyPoints(points: LatencyPoint[], options: PeakCutOptions = {}): LatencyPoint[] {
  const cutMax = peakCutMax(points, options.minSamples ?? 4)
  if (cutMax === null) return points

  return points.map((point) => {
    if (point.medianMs === null || point.medianMs <= cutMax) return point
    return { ...point, medianMs: cutMax }
  })
}

export function yDomain(points: LatencyPoint[], options: PeakCutOptions = {}): { min: number; max: number } {
  if (options.peakCut) {
    const cutMax = peakCutMax(points, options.minSamples ?? 4)
    if (cutMax !== null) return { min: 0, max: cutMax }
  }

  const values = points
    .map((point) => point.medianMs)
    .filter((value): value is number => value !== null && value !== undefined)
  if (values.length === 0) {
    return { min: 0, max: 1 }
  }
  const max = Math.max(...values)
  return { min: 0, max: Math.max(1, Math.ceil(max * 1.2)) }
}

function peakCutMax(points: LatencyPoint[], minSamples: number): number | null {
  if (points.length < minSamples) return null

  const values = points
    .map((point) => point.medianMs)
    .filter((value): value is number => value !== null && value !== undefined)
    .sort((a, b) => a - b)
  if (values.length < 2) return null

  const top = values.at(-1)!
  const previous = values.at(-2)!
  const cap = Math.max(1, Math.ceil(previous * 1.2))
  return top > cap ? cap : null
}
