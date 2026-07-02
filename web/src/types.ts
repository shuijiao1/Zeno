export type NodeStatus = 'online' | 'warning' | 'offline' | 'no_data'
export type ProbeType = 'ping' | 'tcping'

export interface LatencySummary {
  targetId: string
  targetName: string
  medianMs: number | null
  avgMs: number | null
  lossPercent: number | null
  updatedAt: string
}

export interface HomeCardNode {
  id: string
  displayName: string
  status: NodeStatus
  os: 'debian' | 'ubuntu' | 'centos' | 'alpine' | 'linux' | 'unknown'
  countryCode?: string
  subtitle?: string
  cpuCores?: number | null
  expiryLabel?: string
  cpuPercent: number | null
  memoryUsedBytes: number | null
  memoryTotalBytes: number | null
  diskUsedBytes: number | null
  diskTotalBytes: number | null
  netInSpeedBps: number | null
  netOutSpeedBps: number | null
  netInTotalBytes: number | null
  netOutTotalBytes: number | null
  monthlyBillableBytes: number | null
  monthlyQuotaBytes: number | null
  latencySummary?: LatencySummary
}

export interface LatencyPoint {
  ts: string
  targetId: string
  targetName: string
  medianMs: number | null
  lossPercent: number
}
