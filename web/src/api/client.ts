import type { AdminNode, AdminNodeInstallCommand, AdminProbeTarget, HomeCardNode, LatencyPoint, StatePoint } from '../types'

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

interface ApiAdminNode {
  id: string
  display_name: string
  status: string
  country_code?: string
  region?: string
  disabled: boolean
  billing_mode: string
  monthly_quota_bytes?: number | null
  last_seen_at?: string | null
  created_at: string
  updated_at: string
  hostname?: string
  os_name?: string
  os_version?: string
  kernel?: string
  arch?: string
  virtualization?: string
  cpu_model?: string
  cpu_cores?: number | null
  memory_total_bytes?: number | null
  disk_total_bytes?: number | null
  boot_time?: string | null
  agent_version?: string
}

interface ApiAdminProbeTargetAssignment {
  node_id: string
  node_display_name: string
  enabled: boolean
}

interface ApiAdminProbeTarget {
  id: string
  name: string
  type: 'ping' | 'tcping'
  address: string
  port: number | null
  count: number
  timeout_ms: number
  interval_sec: number
  enabled: boolean
  assignments: ApiAdminProbeTargetAssignment[]
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

export interface ApiAdminNodesResponse {
  nodes: ApiAdminNode[]
}

export interface ApiAdminNodeResponse {
  node: ApiAdminNode
}

export interface ApiAdminNodeInstallCommandResponse {
  node_id: string
  command: string
}

export interface ApiAdminProbeTargetsResponse {
  targets: ApiAdminProbeTarget[]
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

export interface AdminNodesData {
  nodes: AdminNode[]
}

export interface AdminProbeTargetsData {
  targets: AdminProbeTarget[]
}

export interface AdminNodeUpdateInput {
  displayName?: string
  countryCode?: string
  region?: string
  monthlyQuotaBytes?: number | null
  disabled?: boolean
}

export interface AdminNodeCreateInput {
  id?: string
  displayName: string
  countryCode?: string
  region?: string
  monthlyQuotaBytes?: number | null
  disabled?: boolean
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

export async function fetchAdminNodes(adminToken: string): Promise<AdminNodesData> {
  const response = await fetch('/api/admin/v1/nodes', {
    headers: {
      Accept: 'application/json',
      'X-Admin-Token': adminToken,
    },
  })
  if (!response.ok) {
    throw new Error(`admin nodes request failed: ${response.status}`)
  }
  return normalizeAdminNodes(await response.json() as ApiAdminNodesResponse)
}

export async function fetchAdminProbeTargets(adminToken: string): Promise<AdminProbeTargetsData> {
  const response = await fetch('/api/admin/v1/probe-targets', {
    headers: {
      Accept: 'application/json',
      'X-Admin-Token': adminToken,
    },
  })
  if (!response.ok) {
    throw new Error(`admin probe targets request failed: ${response.status}`)
  }
  return normalizeAdminProbeTargets(await response.json() as ApiAdminProbeTargetsResponse)
}

export async function createAdminNode(adminToken: string, input: AdminNodeCreateInput): Promise<AdminNode> {
  const response = await fetch('/api/admin/v1/nodes', {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-Admin-Token': adminToken,
    },
    body: JSON.stringify(serializeAdminNodeCreate(input)),
  })
  if (!response.ok) {
    throw new Error(`admin node create failed: ${response.status}`)
  }
  const data = await response.json() as ApiAdminNodeResponse
  return normalizeAdminNode(data.node)
}

export async function requestAdminNodeInstallCommand(adminToken: string, nodeId: string): Promise<AdminNodeInstallCommand> {
  const response = await fetch(`/api/admin/v1/nodes/${encodeURIComponent(nodeId)}/install-command`, {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'X-Admin-Token': adminToken,
    },
  })
  if (!response.ok) {
    throw new Error(`admin node install command failed: ${response.status}`)
  }
  const data = await response.json() as ApiAdminNodeInstallCommandResponse
  return { nodeId: data.node_id, command: data.command }
}

export async function updateAdminNode(adminToken: string, nodeId: string, input: AdminNodeUpdateInput): Promise<AdminNode> {
  const response = await fetch(`/api/admin/v1/nodes/${encodeURIComponent(nodeId)}`, {
    method: 'PATCH',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-Admin-Token': adminToken,
    },
    body: JSON.stringify(serializeAdminNodeUpdate(input)),
  })
  if (!response.ok) {
    throw new Error(`admin node update failed: ${response.status}`)
  }
  const data = await response.json() as ApiAdminNodeResponse
  return normalizeAdminNode(data.node)
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

export function normalizeAdminNodes(input: ApiAdminNodesResponse): AdminNodesData {
  return {
    nodes: input.nodes.map(normalizeAdminNode),
  }
}

export function normalizeAdminProbeTargets(input: ApiAdminProbeTargetsResponse): AdminProbeTargetsData {
  return {
    targets: input.targets.map(normalizeAdminProbeTarget),
  }
}

function serializeAdminNodeUpdate(input: AdminNodeUpdateInput) {
  return {
    ...(input.displayName !== undefined ? { display_name: input.displayName } : {}),
    ...(input.countryCode !== undefined ? { country_code: input.countryCode } : {}),
    ...(input.region !== undefined ? { region: input.region } : {}),
    ...(input.monthlyQuotaBytes !== undefined ? { monthly_quota_bytes: input.monthlyQuotaBytes } : {}),
    ...(input.disabled !== undefined ? { disabled: input.disabled } : {}),
  }
}

function serializeAdminNodeCreate(input: AdminNodeCreateInput) {
  return {
    ...(input.id !== undefined && input.id.trim() !== '' ? { id: input.id } : {}),
    display_name: input.displayName,
    ...(input.countryCode !== undefined ? { country_code: input.countryCode } : {}),
    ...(input.region !== undefined ? { region: input.region } : {}),
    ...(input.monthlyQuotaBytes !== undefined ? { monthly_quota_bytes: input.monthlyQuotaBytes } : {}),
    ...(input.disabled !== undefined ? { disabled: input.disabled } : {}),
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

function normalizeAdminNode(node: ApiAdminNode): AdminNode {
  return {
    id: node.id,
    displayName: node.display_name,
    status: node.status,
    countryCode: node.country_code,
    region: node.region,
    disabled: node.disabled,
    billingMode: node.billing_mode,
    monthlyQuotaBytes: node.monthly_quota_bytes ?? null,
    lastSeenAt: node.last_seen_at ?? undefined,
    createdAt: node.created_at,
    updatedAt: node.updated_at,
    hostname: node.hostname,
    osName: node.os_name,
    osVersion: node.os_version,
    kernel: node.kernel,
    arch: node.arch,
    virtualization: node.virtualization,
    cpuModel: node.cpu_model,
    cpuCores: node.cpu_cores ?? null,
    memoryTotalBytes: node.memory_total_bytes ?? null,
    diskTotalBytes: node.disk_total_bytes ?? null,
    bootTime: node.boot_time ?? undefined,
    agentVersion: node.agent_version,
  }
}

function normalizeAdminProbeTarget(target: ApiAdminProbeTarget): AdminProbeTarget {
  return {
    id: target.id,
    name: target.name,
    type: target.type,
    address: target.address,
    port: target.port ?? null,
    count: target.count,
    timeoutMs: target.timeout_ms,
    intervalSec: target.interval_sec,
    enabled: target.enabled,
    assignments: target.assignments.map((assignment) => ({
      nodeId: assignment.node_id,
      nodeDisplayName: assignment.node_display_name,
      enabled: assignment.enabled,
    })),
  }
}
