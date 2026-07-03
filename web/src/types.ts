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
  os: 'debian' | 'ubuntu' | 'centos' | 'alpine' | 'linux' | 'windows' | 'unknown'
  arch?: string
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

export interface StatePoint {
  ts: string
  cpuPercent: number | null
  memoryUsedBytes: number | null
  memoryTotalBytes: number | null
  diskUsedBytes: number | null
  diskTotalBytes: number | null
  netInTotalBytes: number | null
  netOutTotalBytes: number | null
  netInSpeedBps: number | null
  netOutSpeedBps: number | null
  uptimeSeconds: number | null
}

export interface AdminNode {
  id: string
  displayName: string
  status: string
  countryCode?: string
  region?: string
  disabled: boolean
  billingMode: string
  monthlyQuotaBytes: number | null
  lastSeenAt?: string
  createdAt: string
  updatedAt: string
  hostname?: string
  osName?: string
  osVersion?: string
  kernel?: string
  arch?: string
  virtualization?: string
  cpuModel?: string
  cpuCores: number | null
  memoryTotalBytes: number | null
  diskTotalBytes: number | null
  bootTime?: string
  agentVersion?: string
}

export interface AdminProbeTargetAssignment {
  nodeId: string
  nodeDisplayName: string
  enabled: boolean
}

export interface AdminProbeTarget {
  id: string
  name: string
  type: ProbeType
  address: string
  port: number | null
  count: number
  timeoutMs: number
  intervalSec: number
  enabled: boolean
  assignments: AdminProbeTargetAssignment[]
}
