import type { LatencyPoint } from '../types'

export interface KulinSeriesPoint {
  created_at: number
  avg_delay: number | null
  packet_loss: number
}

export interface KulinTargetSeries {
  targetId: string
  targetName: string
  points: KulinSeriesPoint[]
}

export interface KulinChartRow {
  created_at: number
  [key: string]: number | null
}

export interface KulinChartView {
  rows: KulinChartRow[]
  lineKeys: string[]
  showPacketLossArea: boolean
}

export function calculateKulinPacketLoss(delays: Array<number | null | undefined>): number[] {
  if (!delays || delays.length === 0) return []

  const packetLossRates: number[] = []
  const windowSize = Math.min(10, Math.max(3, Math.floor(delays.length / 10)))
  const timeoutThreshold = 3000
  const extremeDelayThreshold = 10000

  for (let i = 0; i < delays.length; i += 1) {
    const currentDelay = delays[i]
    let lossRate = 0

    if (currentDelay === 0 || currentDelay === null || currentDelay === undefined) {
      lossRate = 100
    } else if (currentDelay >= extremeDelayThreshold) {
      lossRate = Math.min(95, 60 + (currentDelay - extremeDelayThreshold) / 1000)
    } else if (currentDelay >= timeoutThreshold) {
      lossRate = Math.min(50, (currentDelay - timeoutThreshold) / 200)
    } else {
      const start = Math.max(0, i - Math.floor(windowSize / 2))
      const end = Math.min(delays.length, i + Math.ceil(windowSize / 2))
      const windowDelays = delays.slice(start, end).filter((delay): delay is number => typeof delay === 'number' && delay > 0)

      if (windowDelays.length > 2) {
        const mean = windowDelays.reduce((sum, delay) => sum + delay, 0) / windowDelays.length
        const variance = windowDelays.reduce((sum, delay) => sum + (delay - mean) ** 2, 0) / windowDelays.length
        const standardDeviation = Math.sqrt(variance)
        const coefficientOfVariation = standardDeviation / mean

        if (coefficientOfVariation > 0.8) {
          lossRate = Math.min(25, coefficientOfVariation * 15)
        } else if (coefficientOfVariation > 0.5) {
          lossRate = Math.min(10, coefficientOfVariation * 8)
        } else if (coefficientOfVariation > 0.3) {
          lossRate = Math.min(5, coefficientOfVariation * 5)
        }

        if (currentDelay > mean * 2.5) {
          lossRate += Math.min(15, (currentDelay / mean - 2.5) * 10)
        }
      }
    }

    if (i > 0) {
      const alpha = 0.3
      lossRate = alpha * lossRate + (1 - alpha) * packetLossRates[i - 1]
    }

    packetLossRates.push(Math.max(0, Math.min(100, lossRate)))
  }

  return packetLossRates.map((rate) => Number(rate.toFixed(2)))
}

export function buildKulinTargetSeries(points: LatencyPoint[]): KulinTargetSeries[] {
  const order: string[] = []
  const byTarget = new Map<string, LatencyPoint[]>()

  for (const point of points) {
    if (!byTarget.has(point.targetId)) {
      byTarget.set(point.targetId, [])
      order.push(point.targetId)
    }
    byTarget.get(point.targetId)!.push(point)
  }

  return order.map((targetId) => {
    const targetPoints = [...byTarget.get(targetId)!].sort((a, b) => a.ts.localeCompare(b.ts))
    const delays = targetPoints.map((point) => latencyDelay(point))
    const calculatedPacketLoss = calculateKulinPacketLoss(delays)

    return {
      targetId,
      targetName: targetPoints[0]?.targetName ?? targetId,
      points: targetPoints.map((point, index) => ({
        created_at: Date.parse(point.ts),
        avg_delay: latencyDelay(point),
        packet_loss: Number.isFinite(point.lossPercent) ? point.lossPercent : calculatedPacketLoss[index],
      })),
    }
  })
}

function latencyDelay(point: LatencyPoint): number {
  return typeof point.avgMs === 'number' && Number.isFinite(point.avgMs) ? point.avgMs : 0
}

export function buildKulinChartRows(series: KulinTargetSeries[]): KulinChartRow[] {
  const allTimes = new Set<number>()
  const pointsByTargetTime = new Map<string, Map<number, KulinSeriesPoint>>()
  for (const target of series) {
    const pointsByTime = new Map<number, KulinSeriesPoint>()
    for (const point of target.points) {
      allTimes.add(point.created_at)
      pointsByTime.set(point.created_at, point)
    }
    pointsByTargetTime.set(target.targetId, pointsByTime)
  }

  const rows = Array.from(allTimes)
    .sort((a, b) => a - b)
    .map((createdAt) => {
      const row: KulinChartRow = { created_at: createdAt }
      for (const target of series) {
        const point = pointsByTargetTime.get(target.targetId)?.get(createdAt)
        row[target.targetName] = point ? point.avg_delay : null
        row[`${target.targetName}_packet_loss`] = point ? point.packet_loss : null
      }
      return row
    })

  return rows
}

export function selectKulinChartView(series: KulinTargetSeries[], rows: KulinChartRow[], activeTargetNames: string[]): KulinChartView {
  if (activeTargetNames.length === 1) {
    const selectedName = activeTargetNames[0]
    const selected = series.find((target) => target.targetName === selectedName)
    return {
      rows: selected ? selected.points.map((point) => ({
        created_at: point.created_at,
        avg_delay: point.avg_delay,
        packet_loss: point.packet_loss,
      })) : [],
      lineKeys: ['avg_delay'],
      showPacketLossArea: true,
    }
  }

  return {
    rows,
    lineKeys: activeTargetNames.length > 1 ? activeTargetNames : series.map((target) => target.targetName),
    showPacketLossArea: false,
  }
}

export function applyKulinPeakCut(rows: KulinChartRow[], keysToProcess: string[]): KulinChartRow[] {
  const windowSize = 11
  const alpha = 0.3
  const ewmaHistory: Record<string, number> = {}

  return rows.map((point, index) => {
    if (index < windowSize - 1) return point

    const window = rows.slice(index - windowSize + 1, index + 1)
    const smoothed: KulinChartRow = { ...point }

    for (const key of keysToProcess) {
      const values = window
        .map((row) => row[key])
        .filter((value): value is number => typeof value === 'number' && Number.isFinite(value))

      if (values.length > 0) {
        const processed = processKulinPeakValues(values, alpha)
        if (processed !== null) {
          if (ewmaHistory[key] === undefined) {
            ewmaHistory[key] = processed
          } else {
            ewmaHistory[key] = alpha * processed + (1 - alpha) * ewmaHistory[key]
          }
          smoothed[key] = ewmaHistory[key]
        }
      }
    }

    return smoothed
  })
}

function processKulinPeakValues(values: number[], alpha: number): number | null {
  if (values.length === 0) return null

  const median = medianOf(values)
  const deviations = values.map((value) => Math.abs(value - median))
  const medianDeviation = medianOf(deviations) * 1.4826
  const validValues = values.filter((value) => Math.abs(value - median) <= 3 * medianDeviation && value <= median * 3)

  if (validValues.length === 0) return median

  let ewma = validValues[0]
  for (let i = 1; i < validValues.length; i += 1) {
    ewma = alpha * validValues[i] + (1 - alpha) * ewma
  }
  return ewma
}

function medianOf(values: number[]): number {
  const sorted = [...values].sort((a, b) => a - b)
  const mid = Math.floor(sorted.length / 2)
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2
}
