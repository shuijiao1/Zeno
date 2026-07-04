export type NodeStatus = 'online' | 'warning' | 'offline' | 'no_data'
export type ProbeType = 'ping' | 'tcping' | 'http_get'
export type AdminTheme = 'system' | 'dark' | 'light'

export interface AdminSettings {
  siteTitle: string
  siteSubtitle: string
  logoUrl: string
  theme: AdminTheme
  backgroundUrl: string
  desktopBackgroundUrl: string
  mobileBackgroundUrl: string
  updatedAt?: string
}

export interface AdminAsset {
  id: string
  filename: string
  contentType: string
  sizeBytes: number
  url: string
  createdAt: string
}

export interface AdminMaintenanceSettings {
  enabled: boolean
  stateRetentionDays: number
  probeRetentionDays: number
  notificationRetentionDays: number
  updatedAt?: string
}

export interface AdminMaintenanceStats {
  stateSamples: number
  probeRounds: number
  probeSamples: number
  notificationDeliveries: number
}

export interface AdminMaintenance {
  settings: AdminMaintenanceSettings
  candidates: AdminMaintenanceStats
}

export interface AdminMaintenanceCleanup {
  settings: AdminMaintenanceSettings
  deleted: AdminMaintenanceStats
  candidates: AdminMaintenanceStats
  dryRun: boolean
}

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
  osVersion?: string
  kernel?: string
  arch?: string
  virtualization?: string
  cpuModel?: string
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
  load1: number | null
  load5: number | null
  load15: number | null
  memoryUsedBytes: number | null
  memoryTotalBytes: number | null
  swapUsedBytes: number | null
  swapTotalBytes: number | null
  diskUsedBytes: number | null
  diskTotalBytes: number | null
  netInTotalBytes: number | null
  netOutTotalBytes: number | null
  netInSpeedBps: number | null
  netOutSpeedBps: number | null
  processCount: number | null
  tcpConnectionCount: number | null
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
  monthlyResetDay: number
  expiryDate?: string
  billingCycle?: string
  displayOrder: number
  publicIPv4?: string
  publicIPv6?: string
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

export interface AdminNodeInstallCommand {
  nodeId: string
  command: string
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
  displayOrder: number
  enabled: boolean
  assignments: AdminProbeTargetAssignment[]
}

export interface AdminNotificationChannel {
  id: string
  name: string
  destination: string
  credentialSet: boolean
  enabled: boolean
  createdAt: string
  updatedAt: string
}

export interface AdminNotificationDelivery {
  id: number
  eventType: string
  label: string
  nodeId: string
  nodeName: string
  previousStatus: string
  status: string
  channelId: string
  channelName: string
  success: boolean
  error?: string
  createdAt: string
}

export interface AdminNotificationType {
  eventType: string
  label: string
  enabled: boolean
  updatedAt?: string
}

export interface AdminAlertRule {
  id: string
  name: string
  category: string
  metric: string
  comparator: string
  threshold: number
  thresholdUnit: string
  durationSec: number
  enabled: boolean
  notificationEventType: string
  notificationLabel: string
  description: string
  scopeNodeIds: string[]
  createdAt: string
  updatedAt: string
}

export interface AdminAlertRuleState {
  nodeId: string
  nodeName: string
  nodeStatus: string
  ruleId: string
  ruleName: string
  category: string
  metric: string
  comparator: string
  threshold: number
  thresholdUnit: string
  durationSec: number
  enabled: boolean
  lastValue: number | null
  active: boolean
  notificationEventType: string
  notificationLabel: string
  firstSeenAt: string
  lastSeenAt: string
  updatedAt: string
}
