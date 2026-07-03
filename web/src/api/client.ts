import type { AdminNode, AdminNodeInstallCommand, AdminNotificationChannel, AdminNotificationDelivery, AdminNotificationType, AdminProbeTarget, HomeCardNode, LatencyPoint, StatePoint } from '../types'

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
  os_version?: string
  kernel?: string
  arch?: string
  virtualization?: string
  cpu_model?: string
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
  assignments: ApiAdminProbeTargetAssignment[] | null
}

interface ApiAdminNotificationChannel {
  id: string
  name: string
  type: 'telegram' | 'webhook'
  destination: string
  credential_set: boolean
  enabled: boolean
  created_at: string
  updated_at: string
}

interface ApiAdminNotificationType {
  event_type: string
  label: string
  enabled: boolean
  updated_at?: string
}

interface ApiAdminNotificationDelivery {
  id: number
  event_type: string
  label: string
  node_id: string
  node_name: string
  previous_status: string
  status: string
  channel_id: string
  channel_name: string
  channel_type: 'telegram' | 'webhook'
  success: boolean
  error?: string
  created_at: string
}

export interface ApiSummaryResponse {
  nodes: ApiNode[] | null
  latency_points: ApiLatencyPoint[] | null
}

export interface ApiLatencyResponse {
  node_id: string
  range: string
  points: ApiLatencyPoint[] | null
}

export interface ApiStateResponse {
  node_id: string
  range: string
  points: ApiStatePoint[] | null
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

export interface ApiAdminProbeTargetResponse {
  target: ApiAdminProbeTarget
}

export interface ApiAdminNotificationChannelsResponse {
  channels: ApiAdminNotificationChannel[]
}

export interface ApiAdminNotificationChannelResponse {
  channel: ApiAdminNotificationChannel
}

export interface ApiAdminNotificationTypesResponse {
  types: ApiAdminNotificationType[]
}

export interface ApiAdminNotificationDeliveriesResponse {
  deliveries: ApiAdminNotificationDelivery[]
}

export interface ApiAdminNotificationTestResponse {
  delivery: ApiAdminNotificationDelivery
}

export interface ApiAdminNotificationTypeResponse {
  type: ApiAdminNotificationType
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

export interface AdminNotificationChannelsData {
  channels: AdminNotificationChannel[]
}

export interface AdminNotificationTypesData {
  types: AdminNotificationType[]
}

export interface AdminNotificationDeliveriesData {
  deliveries: AdminNotificationDelivery[]
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

export interface AdminProbeTargetInput {
  id?: string
  name: string
  type: 'tcping'
  address: string
  port: number
  count: number
  timeoutMs: number
  intervalSec: number
  enabled?: boolean
}

export interface AdminProbeTargetUpdateInput {
  name?: string
  type?: 'tcping'
  address?: string
  port?: number
  count?: number
  timeoutMs?: number
  intervalSec?: number
  enabled?: boolean
  assignments?: Array<{ nodeId: string; enabled: boolean }>
}

export interface AdminNotificationChannelCreateInput {
  id?: string
  name: string
  type: 'telegram' | 'webhook'
  destination: string
  credential: string
  enabled?: boolean
}

export interface AdminNotificationChannelUpdateInput {
  name?: string
  type?: 'telegram' | 'webhook'
  destination?: string
  credential?: string
  enabled?: boolean
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

export async function fetchAdminNotificationChannels(adminToken: string): Promise<AdminNotificationChannelsData> {
  const response = await fetch('/api/admin/v1/notification-channels', {
    headers: {
      Accept: 'application/json',
      'X-Admin-Token': adminToken,
    },
  })
  if (!response.ok) {
    throw new Error(`admin notification channels request failed: ${response.status}`)
  }
  return normalizeAdminNotificationChannels(await response.json() as ApiAdminNotificationChannelsResponse)
}

export async function fetchAdminNotificationTypes(adminToken: string): Promise<AdminNotificationTypesData> {
  const response = await fetch('/api/admin/v1/notification-types', {
    headers: {
      Accept: 'application/json',
      'X-Admin-Token': adminToken,
    },
  })
  if (!response.ok) {
    throw new Error(`admin notification types request failed: ${response.status}`)
  }
  return normalizeAdminNotificationTypes(await response.json() as ApiAdminNotificationTypesResponse)
}

export async function fetchAdminNotificationDeliveries(adminToken: string): Promise<AdminNotificationDeliveriesData> {
  const response = await fetch('/api/admin/v1/notification-deliveries', {
    headers: {
      Accept: 'application/json',
      'X-Admin-Token': adminToken,
    },
  })
  if (!response.ok) {
    throw new Error(`admin notification deliveries request failed: ${response.status}`)
  }
  return normalizeAdminNotificationDeliveries(await response.json() as ApiAdminNotificationDeliveriesResponse)
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

export async function createAdminProbeTarget(adminToken: string, input: AdminProbeTargetInput): Promise<AdminProbeTarget> {
  const response = await fetch('/api/admin/v1/probe-targets', {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-Admin-Token': adminToken,
    },
    body: JSON.stringify(serializeAdminProbeTargetCreate(input)),
  })
  if (!response.ok) {
    throw new Error(`admin probe target create failed: ${response.status}`)
  }
  const data = await response.json() as ApiAdminProbeTargetResponse
  return normalizeAdminProbeTarget(data.target)
}

export async function updateAdminProbeTarget(adminToken: string, targetId: string, input: AdminProbeTargetUpdateInput): Promise<AdminProbeTarget> {
  const response = await fetch(`/api/admin/v1/probe-targets/${encodeURIComponent(targetId)}`, {
    method: 'PATCH',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-Admin-Token': adminToken,
    },
    body: JSON.stringify(serializeAdminProbeTargetUpdate(input)),
  })
  if (!response.ok) {
    throw new Error(`admin probe target update failed: ${response.status}`)
  }
  const data = await response.json() as ApiAdminProbeTargetResponse
  return normalizeAdminProbeTarget(data.target)
}

export async function createAdminNotificationChannel(adminToken: string, input: AdminNotificationChannelCreateInput): Promise<AdminNotificationChannel> {
  const response = await fetch('/api/admin/v1/notification-channels', {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-Admin-Token': adminToken,
    },
    body: JSON.stringify(serializeAdminNotificationChannelCreate(input)),
  })
  if (!response.ok) {
    throw new Error(`admin notification channel create failed: ${response.status}`)
  }
  const data = await response.json() as ApiAdminNotificationChannelResponse
  return normalizeAdminNotificationChannel(data.channel)
}

export async function updateAdminNotificationChannel(adminToken: string, channelId: string, input: AdminNotificationChannelUpdateInput): Promise<AdminNotificationChannel> {
  const response = await fetch(`/api/admin/v1/notification-channels/${encodeURIComponent(channelId)}`, {
    method: 'PATCH',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-Admin-Token': adminToken,
    },
    body: JSON.stringify(serializeAdminNotificationChannelUpdate(input)),
  })
  if (!response.ok) {
    throw new Error(`admin notification channel update failed: ${response.status}`)
  }
  const data = await response.json() as ApiAdminNotificationChannelResponse
  return normalizeAdminNotificationChannel(data.channel)
}

export async function deleteAdminNotificationChannel(adminToken: string, channelId: string): Promise<void> {
  const response = await fetch(`/api/admin/v1/notification-channels/${encodeURIComponent(channelId)}`, {
    method: 'DELETE',
    headers: {
      Accept: 'application/json',
      'X-Admin-Token': adminToken,
    },
  })
  if (!response.ok) {
    throw new Error(`admin notification channel delete failed: ${response.status}`)
  }
}

export async function testAdminNotificationChannel(adminToken: string, channelId: string): Promise<AdminNotificationDelivery> {
  const response = await fetch(`/api/admin/v1/notification-channels/${encodeURIComponent(channelId)}/test`, {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'X-Admin-Token': adminToken,
    },
  })
  if (!response.ok) {
    throw new Error(`admin notification channel test failed: ${response.status}`)
  }
  const data = await response.json() as ApiAdminNotificationTestResponse
  return normalizeAdminNotificationDelivery(data.delivery)
}

export async function updateAdminNotificationType(adminToken: string, eventType: string, enabled: boolean): Promise<AdminNotificationType> {
  const response = await fetch(`/api/admin/v1/notification-types/${encodeURIComponent(eventType)}`, {
    method: 'PATCH',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-Admin-Token': adminToken,
    },
    body: JSON.stringify({ enabled }),
  })
  if (!response.ok) {
    throw new Error(`admin notification type update failed: ${response.status}`)
  }
  const data = await response.json() as ApiAdminNotificationTypeResponse
  return normalizeAdminNotificationType(data.type)
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
    nodes: (input.nodes ?? []).map(normalizeNode),
    latencyPoints: (input.latency_points ?? []).map(normalizeLatencyPoint),
  }
}

export function normalizeNodeLatency(input: ApiLatencyResponse): NodeLatencyData {
  return {
    nodeId: input.node_id,
    range: input.range,
    points: (input.points ?? []).map(normalizeLatencyPoint),
  }
}

export function normalizeNodeState(input: ApiStateResponse): NodeStateData {
  return {
    nodeId: input.node_id,
    range: input.range,
    points: (input.points ?? []).map(normalizeStatePoint),
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

export function normalizeAdminNotificationChannels(input: ApiAdminNotificationChannelsResponse): AdminNotificationChannelsData {
  return {
    channels: input.channels.map(normalizeAdminNotificationChannel),
  }
}

export function normalizeAdminNotificationTypes(input: ApiAdminNotificationTypesResponse): AdminNotificationTypesData {
  return {
    types: input.types.map(normalizeAdminNotificationType),
  }
}

export function normalizeAdminNotificationDeliveries(input: ApiAdminNotificationDeliveriesResponse): AdminNotificationDeliveriesData {
  return {
    deliveries: (input.deliveries ?? []).map(normalizeAdminNotificationDelivery),
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

function serializeAdminProbeTargetCreate(input: AdminProbeTargetInput) {
  return {
    ...(input.id !== undefined && input.id.trim() !== '' ? { id: input.id } : {}),
    name: input.name,
    type: input.type,
    address: input.address,
    port: input.port,
    count: input.count,
    timeout_ms: input.timeoutMs,
    interval_sec: input.intervalSec,
    ...(input.enabled !== undefined ? { enabled: input.enabled } : {}),
  }
}

function serializeAdminProbeTargetUpdate(input: AdminProbeTargetUpdateInput) {
  return {
    ...(input.name !== undefined ? { name: input.name } : {}),
    ...(input.type !== undefined ? { type: input.type } : {}),
    ...(input.address !== undefined ? { address: input.address } : {}),
    ...(input.port !== undefined ? { port: input.port } : {}),
    ...(input.count !== undefined ? { count: input.count } : {}),
    ...(input.timeoutMs !== undefined ? { timeout_ms: input.timeoutMs } : {}),
    ...(input.intervalSec !== undefined ? { interval_sec: input.intervalSec } : {}),
    ...(input.enabled !== undefined ? { enabled: input.enabled } : {}),
    ...(input.assignments !== undefined ? {
      assignments: input.assignments.map((assignment) => ({
        node_id: assignment.nodeId,
        enabled: assignment.enabled,
      })),
    } : {}),
  }
}

function serializeAdminNotificationChannelCreate(input: AdminNotificationChannelCreateInput) {
  return {
    ...(input.id !== undefined && input.id.trim() !== '' ? { id: input.id } : {}),
    name: input.name,
    type: input.type,
    destination: input.destination,
    credential: input.credential,
    ...(input.enabled !== undefined ? { enabled: input.enabled } : {}),
  }
}

function serializeAdminNotificationChannelUpdate(input: AdminNotificationChannelUpdateInput) {
  return {
    ...(input.name !== undefined ? { name: input.name } : {}),
    ...(input.type !== undefined ? { type: input.type } : {}),
    ...(input.destination !== undefined ? { destination: input.destination } : {}),
    ...(input.credential !== undefined ? { credential: input.credential } : {}),
    ...(input.enabled !== undefined ? { enabled: input.enabled } : {}),
  }
}

function normalizeNode(node: ApiNode): HomeCardNode {
  return {
    id: node.id,
    displayName: node.display_name,
    status: node.status,
    os: node.os,
    osVersion: node.os_version,
    kernel: node.kernel,
    arch: node.arch,
    virtualization: node.virtualization,
    cpuModel: node.cpu_model,
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
    assignments: (target.assignments ?? []).map((assignment) => ({
      nodeId: assignment.node_id,
      nodeDisplayName: assignment.node_display_name,
      enabled: assignment.enabled,
    })),
  }
}

function normalizeAdminNotificationChannel(channel: ApiAdminNotificationChannel): AdminNotificationChannel {
  return {
    id: channel.id,
    name: channel.name,
    type: channel.type,
    destination: channel.destination,
    credentialSet: channel.credential_set,
    enabled: channel.enabled,
    createdAt: channel.created_at,
    updatedAt: channel.updated_at,
  }
}

function normalizeAdminNotificationType(notificationType: ApiAdminNotificationType): AdminNotificationType {
  return {
    eventType: notificationType.event_type,
    label: notificationType.label,
    enabled: notificationType.enabled,
    updatedAt: notificationType.updated_at,
  }
}

function normalizeAdminNotificationDelivery(delivery: ApiAdminNotificationDelivery): AdminNotificationDelivery {
  return {
    id: delivery.id,
    eventType: delivery.event_type,
    label: delivery.label,
    nodeId: delivery.node_id,
    nodeName: delivery.node_name,
    previousStatus: delivery.previous_status,
    status: delivery.status,
    channelId: delivery.channel_id,
    channelName: delivery.channel_name,
    channelType: delivery.channel_type,
    success: delivery.success,
    error: delivery.error,
    createdAt: delivery.created_at,
  }
}
