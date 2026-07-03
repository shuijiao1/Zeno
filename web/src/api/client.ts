import type { HomeCardNode, LatencyPoint, StatePoint } from '../types'

interface ApiLatencySummary {
  target_id: string
  target_name: string
  median_ms: number | null
  avg_ms: number | null
  loss_percent: number | null
  updated_at: string
}

interface ApiNode {
  id: string
  display_name: string
  status: HomeCardNode['status']
  os: HomeCardNode['os']
  arch?: string
  country_code?: string
  subtitle?: string
  cpu_cores?: number | null
  expiry_label?: string
  cpu_percent: number | null
  memory_used_bytes: number | null
  memory_total_bytes: number | null
  disk_used_bytes: number | null
  disk_total_bytes: number | null
  net_in_speed_bps: number | null
  net_out_speed_bps: number | null
  net_in_total_bytes: number | null
  net_out_total_bytes: number | null
  monthly_billable_bytes: number | null
  monthly_quota_bytes: number | null
  latency_summary?: ApiLatencySummary
}

interface ApiLatencyPoint {
  ts: string
  target_id: string
  target_name: string
  median_ms: number | null
  loss_percent: number
}

interface ApiStatePoint {
  ts: string
  cpu_percent: number | null
  memory_used_bytes: number | null
  memory_total_bytes: number | null
  disk_used_bytes: number | null
  disk_total_bytes: number | null
  net_in_total_bytes: number | null
  net_out_total_bytes: number | null
  net_in_speed_bps: number | null
  net_out_speed_bps: number | null
  uptime_seconds: number | null
}

export interface ApiSummaryResponse {
  nodes: ApiNode[]
  latency_points: ApiLatencyPoint[]
}

export interface ApiLatencyResponse {
  node_id: string
  range: string
  points: ApiLatencyPoint[]
}

export interface ApiStateResponse {
  node_id: string
  range: string
  points: ApiStatePoint[]
}

export interface SummaryData {
  nodes: HomeCardNode[]
  latencyPoints: LatencyPoint[]
}

export interface NodeLatencyData {
  nodeId: string
  range: string
  points: LatencyPoint[]
}

export interface NodeStateData {
  nodeId: string
  range: string
  points: StatePoint[]
}

export async function fetchSummary(): Promise<SummaryData> {
  const response = await fetch('/api/public/v1/summary', { headers: { Accept: 'application/json' } })
  if (!response.ok) {
    throw new Error(`summary request failed: ${response.status}`)
  }
  return normalizeSummary(await response.json() as ApiSummaryResponse)
}

export async function fetchNodeLatency(nodeId: string, range = '1h'): Promise<NodeLatencyData> {
  const response = await fetch(`/api/public/v1/nodes/${encodeURIComponent(nodeId)}/latency?range=${encodeURIComponent(range)}`, { headers: { Accept: 'application/json' } })
  if (!response.ok) {
    throw new Error(`latency request failed: ${response.status}`)
  }
  return normalizeNodeLatency(await response.json() as ApiLatencyResponse)
}

export async function fetchNodeState(nodeId: string, range = '1h'): Promise<NodeStateData> {
  const response = await fetch(`/api/public/v1/nodes/${encodeURIComponent(nodeId)}/state?range=${encodeURIComponent(range)}`, { headers: { Accept: 'application/json' } })
  if (!response.ok) {
    throw new Error(`state request failed: ${response.status}`)
  }
  return normalizeNodeState(await response.json() as ApiStateResponse)
}

export function normalizeSummary(input: ApiSummaryResponse): SummaryData {
  return {
    nodes: input.nodes.map(normalizeNode),
    latencyPoints: input.latency_points.map(normalizeLatencyPoint),
  }
}

export function normalizeNodeLatency(input: ApiLatencyResponse): NodeLatencyData {
  return {
    nodeId: input.node_id,
    range: input.range,
    points: input.points.map(normalizeLatencyPoint),
  }
}

export function normalizeNodeState(input: ApiStateResponse): NodeStateData {
  return {
    nodeId: input.node_id,
    range: input.range,
    points: input.points.map(normalizeStatePoint),
  }
}

function normalizeNode(node: ApiNode): HomeCardNode {
  return {
    id: node.id,
    displayName: node.display_name,
    status: node.status,
    os: node.os,
    arch: node.arch,
    countryCode: node.country_code,
    subtitle: node.subtitle,
    cpuCores: node.cpu_cores ?? null,
    expiryLabel: node.expiry_label,
    cpuPercent: node.cpu_percent,
    memoryUsedBytes: node.memory_used_bytes,
    memoryTotalBytes: node.memory_total_bytes,
    diskUsedBytes: node.disk_used_bytes,
    diskTotalBytes: node.disk_total_bytes,
    netInSpeedBps: node.net_in_speed_bps,
    netOutSpeedBps: node.net_out_speed_bps,
    netInTotalBytes: node.net_in_total_bytes,
    netOutTotalBytes: node.net_out_total_bytes,
    monthlyBillableBytes: node.monthly_billable_bytes,
    monthlyQuotaBytes: node.monthly_quota_bytes,
    latencySummary: node.latency_summary ? {
      targetId: node.latency_summary.target_id,
      targetName: node.latency_summary.target_name,
      medianMs: node.latency_summary.median_ms,
      avgMs: node.latency_summary.avg_ms,
      lossPercent: node.latency_summary.loss_percent,
      updatedAt: node.latency_summary.updated_at,
    } : undefined,
  }
}

function normalizeLatencyPoint(point: ApiLatencyPoint): LatencyPoint {
  return {
    ts: point.ts,
    targetId: point.target_id,
    targetName: point.target_name,
    medianMs: point.median_ms,
    lossPercent: point.loss_percent,
  }
}

function normalizeStatePoint(point: ApiStatePoint): StatePoint {
  return {
    ts: point.ts,
    cpuPercent: point.cpu_percent,
    memoryUsedBytes: point.memory_used_bytes,
    memoryTotalBytes: point.memory_total_bytes,
    diskUsedBytes: point.disk_used_bytes,
    diskTotalBytes: point.disk_total_bytes,
    netInTotalBytes: point.net_in_total_bytes,
    netOutTotalBytes: point.net_out_total_bytes,
    netInSpeedBps: point.net_in_speed_bps,
    netOutSpeedBps: point.net_out_speed_bps,
    uptimeSeconds: point.uptime_seconds,
  }
}
