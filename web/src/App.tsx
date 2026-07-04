import { type CSSProperties, type FormEvent, type ReactNode, useEffect, useState } from 'react'
import { createAdminNode, createAdminNotificationChannel, createAdminProbeTarget, deleteAdminNotificationChannel, deleteAdminProbeTarget, fetchAdminAlertRules, fetchAdminAlertRuleStates, fetchAdminMaintenance, fetchAdminNodes, fetchAdminNotificationChannels, fetchAdminNotificationDeliveries, fetchAdminNotificationTypes, fetchAdminProbeTargets, fetchAdminSettings, fetchNodeLatency, fetchNodeState, fetchPublicSettings, fetchSummary, requestAdminNodeInstallCommand, runAdminMaintenanceCleanup, testAdminNotificationChannel, updateAdminAlertRule, updateAdminMaintenance, updateAdminNode, updateAdminNotificationChannel, updateAdminNotificationType, updateAdminProbeTarget, updateAdminSettings, type AdminAlertRuleUpdateInput, type AdminMaintenanceCleanupInput, type AdminMaintenanceUpdateInput, type AdminNodeCreateInput, type AdminNodeUpdateInput, type AdminNotificationChannelCreateInput, type AdminNotificationChannelUpdateInput, type AdminProbeTargetInput, type AdminProbeTargetUpdateInput, type AdminSettingsUpdateInput, type NodeLatencyData, type NodeStateData, type SummaryData } from './api/client'
import { LatencyDetail } from './components/LatencyDetail'
import { ServerCard } from './components/ServerCard'
import { startLiveRefresh } from './lib/liveRefresh'
import { nodePath, parseDashboardRoute, type DashboardRoute } from './lib/route'
import type { AdminAlertRule, AdminAlertRuleState, AdminMaintenance, AdminMaintenanceCleanup, AdminMaintenanceStats, AdminNode, AdminNotificationChannel, AdminNotificationDelivery, AdminNotificationType, AdminProbeTarget, AdminSettings, ProbeType } from './types'

type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; data: SummaryData }
  | { kind: 'error'; message: string }

type LatencyLoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; data: NodeLatencyData }
  | { kind: 'error'; message: string }

type StateHistoryLoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; data: NodeStateData }
  | { kind: 'error'; message: string }

type AdminLoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; nodes: AdminNode[]; targets: AdminProbeTarget[]; notificationChannels: AdminNotificationChannel[]; notificationTypes: AdminNotificationType[]; notificationDeliveries: AdminNotificationDelivery[]; alertRules: AdminAlertRule[]; alertRuleStates: AdminAlertRuleState[]; maintenance: AdminMaintenance }
  | { kind: 'error'; message: string }

type AdminSection = 'overview' | 'nodes' | 'targets' | 'rules' | 'maintenance' | 'settings' | 'notifications'
type AdminTargetSort = 'order' | 'name' | 'status' | 'type' | 'assignments'

function sum(values: Array<number | null | undefined>): number {
  return values.reduce<number>((total, value) => total + (value ?? 0), 0)
}

function compactBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  const digits = unit === 0 ? 0 : 2
  return `${size.toFixed(digits)} ${units[unit]}`
}

function compactRate(value: number): string {
  return `${compactBytes(value)}/s`
}

const defaultSettings: AdminSettings = {
  siteTitle: 'Zeno',
  siteSubtitle: '服务器运行概览',
  logoUrl: '/assets/logo/id.png',
  theme: 'system',
  backgroundUrl: '',
  desktopBackgroundUrl: '',
  mobileBackgroundUrl: '',
}

const emptyAdminMaintenance: AdminMaintenance = {
  settings: {
    enabled: false,
    stateRetentionDays: 30,
    probeRetentionDays: 30,
    notificationRetentionDays: 90,
  },
  candidates: {
    stateSamples: 0,
    probeRounds: 0,
    probeSamples: 0,
    notificationDeliveries: 0,
  },
}

function backgroundImageValue(url: string): string {
  return `linear-gradient(rgba(24, 21, 18, 0.78), rgba(24, 21, 18, 0.78)), url("${url.replaceAll('"', '%22')}")`
}

export function shellStyleForSettings(settings: AdminSettings): CSSProperties | undefined {
  const desktopBackgroundUrl = (settings.desktopBackgroundUrl || settings.backgroundUrl).trim()
  const mobileBackgroundUrl = settings.mobileBackgroundUrl.trim()
  if (desktopBackgroundUrl === '' && mobileBackgroundUrl === '') return undefined
  return {
    '--zeno-desktop-background-image': desktopBackgroundUrl === '' ? 'none' : backgroundImageValue(desktopBackgroundUrl),
    '--zeno-mobile-background-image': mobileBackgroundUrl === '' ? (desktopBackgroundUrl === '' ? 'none' : backgroundImageValue(desktopBackgroundUrl)) : backgroundImageValue(mobileBackgroundUrl),
    backgroundSize: 'cover',
    backgroundAttachment: 'fixed',
  } as CSSProperties
}

export function reconcileAlertRuleStates(updatedRule: AdminAlertRule, states: AdminAlertRuleState[]): AdminAlertRuleState[] {
  return states.map((state) => {
    if (state.ruleId !== updatedRule.id) return state
    const scopeApplies = alertRuleAppliesToNode(updatedRule, state.nodeId)
    return {
      ...state,
      ruleName: updatedRule.name,
      category: updatedRule.category,
      metric: updatedRule.metric,
      comparator: updatedRule.comparator,
      threshold: updatedRule.threshold,
      thresholdUnit: updatedRule.thresholdUnit,
      durationSec: updatedRule.durationSec,
      enabled: updatedRule.enabled,
      active: scopeApplies && updatedRule.enabled && state.nodeStatus !== 'disabled' && alertRuleStateMatchesCurrentThreshold(state, updatedRule.comparator, updatedRule.threshold),
      notificationEventType: updatedRule.notificationEventType,
      notificationLabel: updatedRule.notificationLabel,
      updatedAt: updatedRule.updatedAt,
    }
  })
}

export function reconcileAlertRuleStatesForNode(updatedNode: AdminNode, states: AdminAlertRuleState[], rules: AdminAlertRule[] = []): AdminAlertRuleState[] {
  const rulesById = new Map(rules.map((rule) => [rule.id, rule]))
  return states.map((state) => {
    if (state.nodeId !== updatedNode.id) return state
    const nodeDisabled = updatedNode.disabled || updatedNode.status === 'disabled'
    const rule = rulesById.get(state.ruleId)
    const scopeApplies = rule ? alertRuleAppliesToNode(rule, updatedNode.id) : true
    return {
      ...state,
      nodeName: updatedNode.displayName,
      nodeStatus: updatedNode.status,
      active: scopeApplies && !nodeDisabled && state.enabled && alertRuleStateMatchesCurrentThreshold(state, state.comparator, state.threshold),
    }
  })
}

function alertRuleAppliesToNode(rule: AdminAlertRule, nodeId: string): boolean {
  return rule.scopeNodeIds.length === 0 || rule.scopeNodeIds.includes(nodeId)
}

function alertRuleStateMatchesCurrentThreshold(state: AdminAlertRuleState, comparator: string, threshold: number): boolean {
  if (state.lastValue === null) return state.active
  return alertRuleValueMatches(state.lastValue, comparator, threshold)
}

function alertRuleValueMatches(value: number | null, comparator: string, threshold: number): boolean {
  if (value === null || !Number.isFinite(value)) return false
  switch (comparator.trim()) {
    case '>=':
      return value >= threshold
    case '>':
      return value > threshold
    case '<=':
      return value <= threshold
    case '<':
      return value < threshold
    case '=':
    case '==':
      return value === threshold
    default:
      return false
  }
}

export function App() {
  const [state, setState] = useState<LoadState>({ kind: 'loading' })
  const [route, setRoute] = useState<DashboardRoute>(() => parseDashboardRoute(window.location.pathname))
  const [latencyRange, setLatencyRange] = useState('1d')
  const [latencyState, setLatencyState] = useState<LatencyLoadState>({ kind: 'idle' })
  const [stateHistoryState, setStateHistoryState] = useState<StateHistoryLoadState>({ kind: 'idle' })
  const [adminToken, setAdminToken] = useState(() => window.sessionStorage.getItem('zeno_admin_token') ?? '')
  const [adminState, setAdminState] = useState<AdminLoadState>({ kind: 'idle' })
  const [settings, setSettings] = useState<AdminSettings>(defaultSettings)

  useEffect(() => {
    let cancelled = false
    fetchPublicSettings()
      .then((loadedSettings) => {
        if (!cancelled) setSettings(loadedSettings)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    const loadSummary = () => {
      fetchSummary()
        .then((data) => {
          if (!cancelled) setState({ kind: 'ready', data })
        })
        .catch((error: unknown) => {
          if (!cancelled) setState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
        })
    }

    loadSummary()
    const stopRefresh = startLiveRefresh(loadSummary)
    return () => {
      cancelled = true
      stopRefresh()
    }
  }, [])

  useEffect(() => {
    const handlePopState = () => setRoute(parseDashboardRoute(window.location.pathname))
    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
  }, [])

  useEffect(() => {
    if (route.kind !== 'node') {
      setLatencyState({ kind: 'idle' })
      return
    }

    let cancelled = false
    let loadedOnce = false
    const loadLatency = () => {
      if (!loadedOnce) setLatencyState({ kind: 'loading' })
      fetchNodeLatency(route.nodeId, latencyRange)
        .then((data) => {
          loadedOnce = true
          if (!cancelled) setLatencyState({ kind: 'ready', data })
        })
        .catch((error: unknown) => {
          loadedOnce = true
          if (!cancelled) setLatencyState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
        })
    }

    loadLatency()
    const stopRefresh = startLiveRefresh(loadLatency)
    return () => {
      cancelled = true
      stopRefresh()
    }
  }, [route, latencyRange])

  useEffect(() => {
    if (route.kind !== 'node') {
      setStateHistoryState({ kind: 'idle' })
      return
    }

    let cancelled = false
    let loadedOnce = false
    const loadStateHistory = () => {
      if (!loadedOnce) setStateHistoryState({ kind: 'loading' })
      fetchNodeState(route.nodeId, latencyRange)
        .then((data) => {
          loadedOnce = true
          if (!cancelled) setStateHistoryState({ kind: 'ready', data })
        })
        .catch((error: unknown) => {
          loadedOnce = true
          if (!cancelled) setStateHistoryState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
        })
    }

    loadStateHistory()
    const stopRefresh = startLiveRefresh(loadStateHistory)
    return () => {
      cancelled = true
      stopRefresh()
    }
  }, [route, latencyRange])

  useEffect(() => {
    if (route.kind !== 'admin') return
    if (adminToken === '') {
      setAdminState({ kind: 'idle' })
      return
    }

    let cancelled = false
    let loadedOnce = false
    const loadAdminNodes = () => {
      if (!loadedOnce) setAdminState({ kind: 'loading' })
      Promise.all([fetchAdminSettings(adminToken), fetchAdminMaintenance(adminToken), fetchAdminNodes(adminToken), fetchAdminProbeTargets(adminToken), fetchAdminNotificationChannels(adminToken), fetchAdminNotificationTypes(adminToken), fetchAdminNotificationDeliveries(adminToken), fetchAdminAlertRules(adminToken), fetchAdminAlertRuleStates(adminToken)])
        .then(([settingsData, maintenanceData, nodesData, targetsData, channelsData, typesData, deliveriesData, alertRulesData, alertRuleStatesData]) => {
          loadedOnce = true
          if (!cancelled) {
            setSettings(settingsData)
            setAdminState({ kind: 'ready', nodes: nodesData.nodes, targets: targetsData.targets, notificationChannels: channelsData.channels, notificationTypes: typesData.types, notificationDeliveries: deliveriesData.deliveries, alertRules: alertRulesData.rules, alertRuleStates: alertRuleStatesData.states, maintenance: maintenanceData })
          }
        })
        .catch((error: unknown) => {
          loadedOnce = true
          if (!cancelled) setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
        })
    }

    loadAdminNodes()
    const stopRefresh = startLiveRefresh(loadAdminNodes)
    return () => {
      cancelled = true
      stopRefresh()
    }
  }, [route, adminToken])

  const submitAdminToken = (token: string) => {
    const trimmed = token.trim()
    if (trimmed === '') return
    window.sessionStorage.setItem('zeno_admin_token', trimmed)
    setAdminToken(trimmed)
  }

  const clearAdminToken = () => {
    window.sessionStorage.removeItem('zeno_admin_token')
    setAdminToken('')
    setAdminState({ kind: 'idle' })
  }

  const refreshAdminNodes = () => {
    if (adminToken === '') return
    setAdminState({ kind: 'loading' })
    Promise.all([fetchAdminSettings(adminToken), fetchAdminMaintenance(adminToken), fetchAdminNodes(adminToken), fetchAdminProbeTargets(adminToken), fetchAdminNotificationChannels(adminToken), fetchAdminNotificationTypes(adminToken), fetchAdminNotificationDeliveries(adminToken), fetchAdminAlertRules(adminToken), fetchAdminAlertRuleStates(adminToken)])
      .then(([settingsData, maintenanceData, nodesData, targetsData, channelsData, typesData, deliveriesData, alertRulesData, alertRuleStatesData]) => {
        setSettings(settingsData)
        setAdminState({ kind: 'ready', nodes: nodesData.nodes, targets: targetsData.targets, notificationChannels: channelsData.channels, notificationTypes: typesData.types, notificationDeliveries: deliveriesData.deliveries, alertRules: alertRulesData.rules, alertRuleStates: alertRuleStatesData.states, maintenance: maintenanceData })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const createAdminNodeDetails = (input: AdminNodeCreateInput) => {
    if (adminToken === '') return
    createAdminNode(adminToken, input)
      .then((createdNode) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { ...current, nodes: sortAdminNodes([...current.nodes, createdNode]) }
          }
          return { kind: 'ready', nodes: [createdNode], targets: [], notificationChannels: [], notificationTypes: [], notificationDeliveries: [], alertRules: [], alertRuleStates: [], maintenance: emptyAdminMaintenance }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const requestAdminInstallCommand = (nodeId: string): Promise<string> => {
    if (adminToken === '') return Promise.reject(new Error('missing admin token'))
    return requestAdminNodeInstallCommand(adminToken, nodeId).then((result) => result.command)
  }

  const updateAdminNodeDetails = (nodeId: string, input: AdminNodeUpdateInput) => {
    if (adminToken === '') return
    updateAdminNode(adminToken, nodeId, input)
      .then((updatedNode) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return {
              ...current,
              nodes: sortAdminNodes(current.nodes.map((node) => node.id === updatedNode.id ? updatedNode : node)),
              alertRuleStates: reconcileAlertRuleStatesForNode(updatedNode, current.alertRuleStates, current.alertRules),
            }
          }
          return { kind: 'ready', nodes: [updatedNode], targets: [], notificationChannels: [], notificationTypes: [], notificationDeliveries: [], alertRules: [], alertRuleStates: [], maintenance: emptyAdminMaintenance }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const createAdminProbeTargetDetails = (input: AdminProbeTargetInput) => {
    if (adminToken === '') return
    createAdminProbeTarget(adminToken, input)
      .then((createdTarget) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { ...current, targets: sortAdminProbeTargets([...current.targets, createdTarget], 'order') }
          }
          return { kind: 'ready', nodes: [], targets: [createdTarget], notificationChannels: [], notificationTypes: [], notificationDeliveries: [], alertRules: [], alertRuleStates: [], maintenance: emptyAdminMaintenance }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const updateAdminProbeTargetDetails = (targetId: string, input: AdminProbeTargetUpdateInput) => {
    if (adminToken === '') return
    updateAdminProbeTarget(adminToken, targetId, input)
      .then((updatedTarget) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { ...current, targets: sortAdminProbeTargets(current.targets.map((target) => target.id === updatedTarget.id ? updatedTarget : target), 'order') }
          }
          return { kind: 'ready', nodes: [], targets: [updatedTarget], notificationChannels: [], notificationTypes: [], notificationDeliveries: [], alertRules: [], alertRuleStates: [], maintenance: emptyAdminMaintenance }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const deleteAdminProbeTargetDetails = (targetId: string) => {
    if (adminToken === '') return
    deleteAdminProbeTarget(adminToken, targetId)
      .then(() => {
        setAdminState((current) => {
          if (current.kind !== 'ready') return current
          return { ...current, targets: current.targets.filter((target) => target.id !== targetId) }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }


  const createAdminNotificationChannelDetails = (input: AdminNotificationChannelCreateInput) => {
    if (adminToken === '') return
    createAdminNotificationChannel(adminToken, input)
      .then((createdChannel) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { ...current, notificationChannels: [...current.notificationChannels, createdChannel] }
          }
          return { kind: 'ready', nodes: [], targets: [], notificationChannels: [createdChannel], notificationTypes: [], notificationDeliveries: [], alertRules: [], alertRuleStates: [], maintenance: emptyAdminMaintenance }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const updateAdminNotificationChannelDetails = (channelId: string, input: AdminNotificationChannelUpdateInput) => {
    if (adminToken === '') return
    updateAdminNotificationChannel(adminToken, channelId, input)
      .then((updatedChannel) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { ...current, notificationChannels: current.notificationChannels.map((channel) => channel.id === updatedChannel.id ? updatedChannel : channel) }
          }
          return { kind: 'ready', nodes: [], targets: [], notificationChannels: [updatedChannel], notificationTypes: [], notificationDeliveries: [], alertRules: [], alertRuleStates: [], maintenance: emptyAdminMaintenance }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const deleteAdminNotificationChannelDetails = (channelId: string) => {
    if (adminToken === '') return
    deleteAdminNotificationChannel(adminToken, channelId)
      .then(() => {
        setAdminState((current) => {
          if (current.kind !== 'ready') return current
          return { ...current, notificationChannels: current.notificationChannels.filter((channel) => channel.id !== channelId) }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const testAdminNotificationChannelDetails = (channelId: string) => {
    if (adminToken === '') return
    testAdminNotificationChannel(adminToken, channelId)
      .then((delivery) => {
        setAdminState((current) => {
          if (current.kind !== 'ready') return current
          return { ...current, notificationDeliveries: [delivery, ...current.notificationDeliveries].slice(0, 50) }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const updateAdminNotificationTypeDetails = (eventType: string, enabled: boolean) => {
    if (adminToken === '') return
    updateAdminNotificationType(adminToken, eventType, enabled)
      .then((updatedType) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { ...current, notificationTypes: current.notificationTypes.map((notificationType) => notificationType.eventType === updatedType.eventType ? updatedType : notificationType) }
          }
          return { kind: 'ready', nodes: [], targets: [], notificationChannels: [], notificationTypes: [updatedType], notificationDeliveries: [], alertRules: [], alertRuleStates: [], maintenance: emptyAdminMaintenance }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const updateAdminAlertRuleDetails = (ruleId: string, input: AdminAlertRuleUpdateInput) => {
    if (adminToken === '') return
    updateAdminAlertRule(adminToken, ruleId, input)
      .then((updatedRule) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return {
              ...current,
              alertRules: current.alertRules.map((rule) => rule.id === updatedRule.id ? updatedRule : rule),
              alertRuleStates: reconcileAlertRuleStates(updatedRule, current.alertRuleStates),
            }
          }
          return { kind: 'ready', nodes: [], targets: [], notificationChannels: [], notificationTypes: [], notificationDeliveries: [], alertRules: [updatedRule], alertRuleStates: [], maintenance: emptyAdminMaintenance }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const updateAdminSettingsDetails = (input: AdminSettingsUpdateInput) => {
    if (adminToken === '') return
    updateAdminSettings(adminToken, input)
      .then((updatedSettings) => setSettings(updatedSettings))
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const updateAdminMaintenanceDetails = (input: AdminMaintenanceUpdateInput) => {
    if (adminToken === '') return
    updateAdminMaintenance(adminToken, input)
      .then((maintenance) => {
        setAdminState((current) => current.kind === 'ready' ? { ...current, maintenance } : current)
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const runAdminMaintenanceCleanupDetails = (input: AdminMaintenanceCleanupInput): Promise<AdminMaintenanceCleanup> => {
    if (adminToken === '') return Promise.reject(new Error('missing admin token'))
    return runAdminMaintenanceCleanup(adminToken, input)
      .then((cleanup) => {
        setAdminState((current) => current.kind === 'ready' ? { ...current, maintenance: { settings: cleanup.settings, candidates: cleanup.candidates } } : current)
        return cleanup
      })
      .catch((error: unknown) => {
        setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
        throw error
      })
  }

  const navigateHome = () => {
    window.history.pushState(null, '', '/')
    setRoute({ kind: 'home' })
  }

  const navigateAdmin = () => {
    window.history.pushState(null, '', '/dashboard')
    setRoute({ kind: 'admin' })
  }

  const navigateNode = (nodeId: string) => {
    window.history.pushState(null, '', nodePath(nodeId))
    setLatencyRange('1d')
    setRoute({ kind: 'node', nodeId })
  }

  const nodes = state.kind === 'ready' ? state.data.nodes : []
  const selectedNode = route.kind === 'node' ? nodes.find((node) => node.id === route.nodeId) : undefined
  const totalCount = nodes.length
  const onlineCount = nodes.filter((node) => node.status === 'online').length
  const offlineCount = nodes.filter((node) => node.status === 'offline').length
  const totalUp = sum(nodes.map((node) => node.netOutTotalBytes))
  const totalDown = sum(nodes.map((node) => node.netInTotalBytes))
  const upSpeed = sum(nodes.map((node) => node.netOutSpeedBps))
  const downSpeed = sum(nodes.map((node) => node.netInSpeedBps))

  return (
    <main className="kulin-shell" data-theme={settings.theme} style={shellStyleForSettings(settings)}>
      {route.kind === 'node' && <DashboardHeader settings={settings} onHome={navigateHome} onAdmin={navigateAdmin} />}

      {route.kind === 'admin' && (
        <AdminDashboard
          onHome={navigateHome}
          settings={settings}
          hasAdminToken={adminToken !== ''}
          adminState={adminState}
          onAdminTokenSubmit={submitAdminToken}
          onAdminTokenClear={clearAdminToken}
          onAdminRefresh={refreshAdminNodes}
          onAdminNodeCreate={createAdminNodeDetails}
          onAdminNodeUpdate={updateAdminNodeDetails}
          onAdminInstallCommand={requestAdminInstallCommand}
          onAdminProbeTargetCreate={createAdminProbeTargetDetails}
          onAdminProbeTargetUpdate={updateAdminProbeTargetDetails}
          onAdminProbeTargetDelete={deleteAdminProbeTargetDetails}
          onAdminNotificationChannelCreate={createAdminNotificationChannelDetails}
          onAdminNotificationChannelUpdate={updateAdminNotificationChannelDetails}
          onAdminNotificationChannelDelete={deleteAdminNotificationChannelDetails}
          onAdminNotificationChannelTest={testAdminNotificationChannelDetails}
          onAdminNotificationTypeUpdate={updateAdminNotificationTypeDetails}
          onAdminAlertRuleUpdate={updateAdminAlertRuleDetails}
          onAdminSettingsUpdate={updateAdminSettingsDetails}
          onAdminMaintenanceUpdate={updateAdminMaintenanceDetails}
          onAdminMaintenanceCleanup={runAdminMaintenanceCleanupDetails}
        />
      )}

      {route.kind !== 'admin' && state.kind === 'loading' && <section className="state-panel">正在读取 Controller API…</section>}
      {route.kind !== 'admin' && state.kind === 'error' && <section className="state-panel is-error">API 读取失败：{state.message}</section>}

      {state.kind === 'ready' && route.kind === 'node' && selectedNode && (
        <LatencyDetail
          node={selectedNode}
          points={latencyState.kind === 'ready' ? latencyState.data.points : []}
          statePoints={stateHistoryState.kind === 'ready' ? stateHistoryState.data.points : []}
          range={latencyRange}
          loading={latencyState.kind === 'loading'}
          error={latencyState.kind === 'error' ? latencyState.message : undefined}
          stateLoading={stateHistoryState.kind === 'loading'}
          stateError={stateHistoryState.kind === 'error' ? stateHistoryState.message : undefined}
          onBack={navigateHome}
          onRangeChange={setLatencyRange}
        />
      )}

      {state.kind === 'ready' && route.kind === 'node' && !selectedNode && (
        <section className="state-panel is-error">没有找到这台服务器：{route.nodeId}</section>
      )}

      {state.kind === 'ready' && route.kind === 'home' && (
        <div className="kulin-container">
          <HomeTopPanel
            settings={settings}
            totalCount={totalCount}
            onlineCount={onlineCount}
            offlineCount={offlineCount}
            totalUp={totalUp}
            totalDown={totalDown}
            upSpeed={upSpeed}
            downSpeed={downSpeed}
            onHome={navigateHome}
            onAdmin={navigateAdmin}
          />

          <section className="server-card-list" aria-label="server cards">
            {nodes.map((node) => <ServerCard key={node.id} node={node} onOpen={navigateNode} />)}
          </section>
        </div>
      )}
    </main>
  )
}

interface HomeOverviewPanelProps {
  settings?: AdminSettings
  totalCount: number
  onlineCount: number
  offlineCount: number
  totalUp: number
  totalDown: number
  upSpeed: number
  downSpeed: number
}

interface DashboardHeaderProps {
  settings?: AdminSettings
  onHome: () => void
  onAdmin: () => void
  adminLabel?: string
}

interface HomeTopPanelProps extends HomeOverviewPanelProps {
  onHome: () => void
  onAdmin: () => void
}

export function HomeTopPanel({ settings = defaultSettings, onHome, onAdmin, ...overview }: HomeTopPanelProps) {
  return (
    <section className="home-top-card" aria-label="homepage control panel">
      <DashboardHeader settings={settings} onHome={onHome} onAdmin={onAdmin} />
      <HomeOverviewPanel settings={settings} {...overview} />
    </section>
  )
}

function DashboardHeader({ settings = defaultSettings, onHome, onAdmin, adminLabel = '后台' }: DashboardHeaderProps) {
  return (
    <header className="kulin-nav">
      <button className="brand" type="button" onClick={onHome}>
        <span className="brand-logo"><img src={settings.logoUrl || defaultSettings.logoUrl} alt={`${settings.siteTitle || 'Zeno'} logo`} /></span>
        <span>{settings.siteTitle || 'Zeno'}</span>
      </button>
      <nav className="nav-actions" aria-label="dashboard actions">
        <button className="login-link" type="button" onClick={onAdmin}>{adminLabel}</button>
        <button className="nav-icon-button is-solid" type="button" aria-label="language"><MapIcon /></button>
        <button className="nav-icon-button" type="button" aria-label="切换主题"><SunIcon /><span className="sr-only">切换主题</span></button>
        <button className="nav-icon-button" type="button" aria-label="background"><ImageMinusIcon /></button>
      </nav>
    </header>
  )
}

interface AdminDashboardProps {
  onHome: () => void
  settings?: AdminSettings
  hasAdminToken?: boolean
  adminState?: AdminLoadState
  initialSection?: AdminSection
  onAdminTokenSubmit?: (token: string) => void
  onAdminTokenClear?: () => void
  onAdminRefresh?: () => void
  onAdminNodeCreate?: (input: AdminNodeCreateInput) => void
  onAdminNodeUpdate?: (nodeId: string, input: AdminNodeUpdateInput) => void
  onAdminInstallCommand?: (nodeId: string) => Promise<string>
  onAdminProbeTargetCreate?: (input: AdminProbeTargetInput) => void
  onAdminProbeTargetUpdate?: (targetId: string, input: AdminProbeTargetUpdateInput) => void
  onAdminProbeTargetDelete?: (targetId: string) => void
  onAdminNotificationChannelCreate?: (input: AdminNotificationChannelCreateInput) => void
  onAdminNotificationChannelUpdate?: (channelId: string, input: AdminNotificationChannelUpdateInput) => void
  onAdminNotificationChannelDelete?: (channelId: string) => void
  onAdminNotificationChannelTest?: (channelId: string) => void
  onAdminNotificationTypeUpdate?: (eventType: string, enabled: boolean) => void
  onAdminAlertRuleUpdate?: (ruleId: string, input: AdminAlertRuleUpdateInput) => void
  onAdminSettingsUpdate?: (input: AdminSettingsUpdateInput) => void
  onAdminMaintenanceUpdate?: (input: AdminMaintenanceUpdateInput) => void
  onAdminMaintenanceCleanup?: (input: AdminMaintenanceCleanupInput) => Promise<AdminMaintenanceCleanup>
}

export function AdminDashboard({
  onHome,
  settings = defaultSettings,
  hasAdminToken = false,
  adminState = { kind: 'idle' },
  initialSection = 'overview',
  onAdminTokenSubmit = () => {},
  onAdminTokenClear = () => {},
  onAdminRefresh = () => {},
  onAdminNodeCreate = () => {},
  onAdminNodeUpdate = () => {},
  onAdminInstallCommand = () => Promise.reject(new Error('install command unavailable')),
  onAdminProbeTargetCreate = () => {},
  onAdminProbeTargetUpdate = () => {},
  onAdminProbeTargetDelete = () => {},
  onAdminNotificationChannelCreate = () => {},
  onAdminNotificationChannelUpdate = () => {},
  onAdminNotificationChannelDelete = () => {},
  onAdminNotificationChannelTest = () => {},
  onAdminNotificationTypeUpdate = () => {},
  onAdminAlertRuleUpdate = () => {},
  onAdminSettingsUpdate = () => {},
  onAdminMaintenanceUpdate = () => {},
  onAdminMaintenanceCleanup = () => Promise.reject(new Error('maintenance cleanup unavailable')),
}: AdminDashboardProps) {
  const [activeSection, setActiveSection] = useState<AdminSection>(initialSection)

  const handleTokenSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const formData = new FormData(form)
    const username = String(formData.get('admin-username') ?? '').trim()
    const password = String(formData.get('admin-password') ?? '').trim()
    if (username !== 'admin' || password === '') return
    onAdminTokenSubmit(password)
    form.reset()
  }

  const nodeCount = adminState.kind === 'ready' ? adminState.nodes.length : 0
  const targetCount = adminState.kind === 'ready' ? adminState.targets.length : 0
  const onlineNodeCount = adminState.kind === 'ready' ? adminState.nodes.filter((node) => node.status === 'online').length : 0
  const enabledTargetCount = adminState.kind === 'ready' ? adminState.targets.filter((target) => target.enabled).length : 0
  const ruleCount = adminState.kind === 'ready' ? adminState.alertRules.length : 0
  const enabledRuleCount = adminState.kind === 'ready' ? adminState.alertRules.filter((rule) => rule.enabled).length : 0
  const maintenanceCandidateCount = adminState.kind === 'ready' ? totalMaintenanceCandidates(adminState.maintenance.candidates) : 0

  return (
    <div className="kulin-container admin-container">
      <section className="home-top-card admin-panel" aria-label="admin dashboard">
        <DashboardHeader settings={settings} onHome={onHome} onAdmin={onHome} adminLabel="前台" />
        <div className="admin-hero">
          <p className="eyebrow">Zeno 后台</p>
          <h2>控制台</h2>
          <p>服务器、延迟监控和通知拆成独立导航；列表只保留关键字段，编辑放进弹窗里处理。</p>
        </div>

        {!hasAdminToken && (
          <>
            <div className="admin-action-grid" aria-label="admin modules">
              <article className="admin-action-card">
                <p>服务器</p>
                <strong>列表 / 弹窗编辑</strong>
              </article>
              <article className="admin-action-card">
                <p>延迟监控</p>
                <strong>目标 / 节点分配</strong>
              </article>
              <article className="admin-action-card">
                <p>状态规则</p>
                <strong>阈值 / 持续时间</strong>
              </article>
              <article className="admin-action-card">
                <p>通知渠道</p>
                <strong>Telegram-only</strong>
              </article>
              <article className="admin-action-card">
                <p>通知类型</p>
                <strong>上线 / 离线 / 异常</strong>
              </article>
            </div>
            <form className="admin-login-card" aria-label="admin login form" onSubmit={handleTokenSubmit}>
              <div>
                <p>后台登录</p>
                <strong>默认账号：admin / admin</strong>
              </div>
              <label>
                <span>账号</span>
                <input name="admin-username" autoComplete="username" placeholder="admin" aria-label="后台账号" />
              </label>
              <label>
                <span>密码</span>
                <input name="admin-password" type="password" autoComplete="current-password" placeholder="admin" aria-label="后台密码" />
              </label>
              <button type="submit">登录后台</button>
            </form>
          </>
        )}

        {hasAdminToken && (
          <>
            <div className="admin-toolbar">
              <AdminSectionNav
                activeSection={activeSection}
                onSectionChange={setActiveSection}
                nodeCount={nodeCount}
                targetCount={targetCount}
                ruleCount={ruleCount}
                maintenanceCandidateCount={maintenanceCandidateCount}
              />
              <div className="admin-section-actions">
                <button type="button" onClick={onAdminRefresh}>刷新</button>
                <button type="button" onClick={onAdminTokenClear}>退出</button>
              </div>
            </div>

            {adminState.kind === 'loading' && <div className="admin-state-card">正在读取 Admin API…</div>}
            {adminState.kind === 'error' && <div className="admin-state-card is-error">Admin API 读取失败：{adminState.message}</div>}

            {adminState.kind === 'ready' && activeSection === 'overview' && (
              <AdminOverviewPanel
                nodeCount={nodeCount}
                onlineNodeCount={onlineNodeCount}
                targetCount={targetCount}
                enabledTargetCount={enabledTargetCount}
                ruleCount={ruleCount}
                enabledRuleCount={enabledRuleCount}
              />
            )}

            {adminState.kind === 'ready' && activeSection === 'nodes' && (
              <AdminNodeSection
                nodes={adminState.nodes}
                onCreate={onAdminNodeCreate}
                onUpdate={onAdminNodeUpdate}
                onInstallCommand={onAdminInstallCommand}
              />
            )}

            {adminState.kind === 'ready' && activeSection === 'targets' && (
              <AdminTargetSection
                targets={adminState.targets}
                nodes={adminState.nodes}
                onCreate={onAdminProbeTargetCreate}
                onUpdate={onAdminProbeTargetUpdate}
                onDelete={onAdminProbeTargetDelete}
              />
            )}

            {adminState.kind === 'ready' && activeSection === 'rules' && (
              <AdminAlertRulesSection
                rules={adminState.alertRules}
                states={adminState.alertRuleStates}
                nodes={adminState.nodes}
                onUpdate={onAdminAlertRuleUpdate}
              />
            )}

            {adminState.kind === 'ready' && activeSection === 'maintenance' && (
              <AdminMaintenanceSection
                maintenance={adminState.maintenance}
                onUpdate={onAdminMaintenanceUpdate}
                onCleanup={onAdminMaintenanceCleanup}
              />
            )}

            {adminState.kind === 'ready' && activeSection === 'settings' && (
              <AdminSettingsSection settings={settings} onUpdate={onAdminSettingsUpdate} />
            )}

            {adminState.kind === 'ready' && activeSection === 'notifications' && (
              <AdminNotificationsSection
                channels={adminState.notificationChannels}
                types={adminState.notificationTypes}
                deliveries={adminState.notificationDeliveries}
                onChannelCreate={onAdminNotificationChannelCreate}
                onChannelUpdate={onAdminNotificationChannelUpdate}
                onChannelDelete={onAdminNotificationChannelDelete}
                onChannelTest={onAdminNotificationChannelTest}
                onTypeUpdate={onAdminNotificationTypeUpdate}
              />
            )}
          </>
        )}
      </section>
    </div>
  )
}

function AdminSectionNav({ activeSection, onSectionChange, nodeCount, targetCount, ruleCount, maintenanceCandidateCount }: { activeSection: AdminSection; onSectionChange: (section: AdminSection) => void; nodeCount: number; targetCount: number; ruleCount: number; maintenanceCandidateCount: number }) {
  const sections: Array<{ id: AdminSection; label: string; meta: string }> = [
    { id: 'overview', label: '概览', meta: 'Summary' },
    { id: 'nodes', label: '服务器', meta: `${nodeCount} 台` },
    { id: 'targets', label: '延迟监控', meta: `${targetCount} 个目标` },
    { id: 'rules', label: '状态规则', meta: `${ruleCount} 条` },
    { id: 'maintenance', label: '数据维护', meta: `${maintenanceCandidateCount} 条候选` },
    { id: 'settings', label: '设置', meta: 'Appearance' },
    { id: 'notifications', label: '通知', meta: 'Channels' },
  ]

  return (
    <nav className="admin-section-nav" aria-label="后台导航">
      {sections.map((section) => (
        <button
          key={section.id}
          type="button"
          data-active={activeSection === section.id}
          onClick={() => onSectionChange(section.id)}
        >
          <span>{section.label}</span>
          <em>{section.meta}</em>
        </button>
      ))}
    </nav>
  )
}

function AdminOverviewPanel({ nodeCount, onlineNodeCount, targetCount, enabledTargetCount, ruleCount, enabledRuleCount }: { nodeCount: number; onlineNodeCount: number; targetCount: number; enabledTargetCount: number; ruleCount: number; enabledRuleCount: number }) {
  return (
    <section className="admin-overview-panel" aria-label="admin overview">
      <div className="admin-action-grid" aria-label="admin modules">
        <article className="admin-action-card">
          <p>服务器</p>
          <strong>{onlineNodeCount} / {nodeCount} 在线</strong>
        </article>
        <article className="admin-action-card">
          <p>延迟监控</p>
          <strong>{enabledTargetCount} / {targetCount} 启用</strong>
        </article>
        <article className="admin-action-card">
          <p>状态规则</p>
          <strong>{enabledRuleCount} / {ruleCount} 启用</strong>
        </article>
        <article className="admin-action-card">
          <p>通知渠道</p>
          <strong>Telegram-only</strong>
        </article>
        <article className="admin-action-card">
          <p>通知类型</p>
          <strong>上线 / 离线 / 异常</strong>
        </article>
      </div>
      <p className="admin-overview-note">从上方导航进入具体模块；列表页只显示关键状态，所有编辑动作都在弹窗中完成。</p>
    </section>
  )
}

function AdminSettingsSection({ settings, onUpdate }: { settings: AdminSettings; onUpdate: (input: AdminSettingsUpdateInput) => void }) {
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const theme = String(formData.get('theme') ?? 'system') as AdminSettings['theme']
    onUpdate({
      siteTitle: String(formData.get('site-title') ?? '').trim(),
      siteSubtitle: String(formData.get('site-subtitle') ?? '').trim(),
      logoUrl: String(formData.get('logo-url') ?? '').trim(),
      theme,
      backgroundUrl: String(formData.get('desktop-background-url') ?? '').trim(),
      desktopBackgroundUrl: String(formData.get('desktop-background-url') ?? '').trim(),
      mobileBackgroundUrl: String(formData.get('mobile-background-url') ?? '').trim(),
    })
  }

  return (
    <section className="admin-settings-section admin-workspace-panel" aria-label="admin settings">
      <header className="admin-section-heading">
        <div>
          <p className="eyebrow">Appearance</p>
          <h3>站点设置</h3>
        </div>
      </header>
      <form className="admin-settings-form admin-node-edit-form" aria-label="外观配置" onSubmit={handleSubmit}>
        <label>
          <span>站点标题</span>
          <input name="site-title" autoComplete="off" defaultValue={settings.siteTitle} />
        </label>
        <label>
          <span>站点副标题</span>
          <input name="site-subtitle" autoComplete="off" defaultValue={settings.siteSubtitle} />
        </label>
        <label>
          <span>头像 / Logo URL</span>
          <input name="logo-url" autoComplete="off" defaultValue={settings.logoUrl} />
        </label>
        <label>
          <span>主题</span>
          <select name="theme" defaultValue={settings.theme}>
            <option value="system">跟随系统</option>
            <option value="dark">深色</option>
            <option value="light">浅色</option>
          </select>
        </label>
        <label>
          <span>电脑端背景图 URL</span>
          <input name="desktop-background-url" autoComplete="off" defaultValue={settings.desktopBackgroundUrl || settings.backgroundUrl} placeholder="可留空" />
        </label>
        <label>
          <span>手机端背景图 URL</span>
          <input name="mobile-background-url" autoComplete="off" defaultValue={settings.mobileBackgroundUrl} placeholder="可留空，默认跟随电脑端" />
        </label>
        {settings.updatedAt && <p className="admin-overview-note">最近更新：{formatAdminDate(settings.updatedAt)}</p>}
        <button type="submit">保存设置</button>
      </form>
    </section>
  )
}

function AdminNodeSection({ nodes, onCreate, onUpdate, onInstallCommand }: { nodes: AdminNode[]; onCreate: (input: AdminNodeCreateInput) => void; onUpdate: (nodeId: string, input: AdminNodeUpdateInput) => void; onInstallCommand: (nodeId: string) => Promise<string> }) {
  const [creatingNode, setCreatingNode] = useState(false)
  const [editingNodeId, setEditingNodeId] = useState<string | null>(null)
  const editingNode = editingNodeId ? nodes.find((node) => node.id === editingNodeId) : undefined
  const applyOrderPatches = (patches: AdminNodeOrderPatch[]) => {
    patches.forEach((patch) => onUpdate(patch.nodeId, { displayOrder: patch.displayOrder }))
  }

  return (
    <section className="admin-node-section admin-workspace-panel" aria-label="admin node list">
      <header className="admin-section-heading">
        <div>
          <p className="eyebrow">Servers</p>
          <h3>服务器列表</h3>
        </div>
        <div className="admin-section-actions">
          <button type="button" onClick={() => applyOrderPatches(buildAdminNodeOrderPatches(nodes))}>整理顺序</button>
          <button className="admin-primary-action" type="button" onClick={() => setCreatingNode(true)}>添加服务器</button>
        </div>
      </header>

      {nodes.length === 0 && <div className="admin-state-card">还没有节点。</div>}
      {nodes.length > 0 && <AdminNodeList nodes={nodes} onEdit={setEditingNodeId} onReorder={(nodeId, direction) => applyOrderPatches(buildAdminNodeOrderPatches(nodes, nodeId, direction))} />}

      {creatingNode && (
        <AdminNodeCreateModal
          onClose={() => setCreatingNode(false)}
          onCreate={(input) => {
            onCreate(input)
            setCreatingNode(false)
          }}
        />
      )}

      {editingNode && (
        <AdminNodeEditModal
          key={editingNode.id}
          node={editingNode}
          onClose={() => setEditingNodeId(null)}
          onUpdate={(nodeId, input) => {
            onUpdate(nodeId, input)
            setEditingNodeId(null)
          }}
          onInstallCommand={onInstallCommand}
        />
      )}
    </section>
  )
}

type AdminNodeOrderDirection = 'up' | 'down'
type AdminNodeOrderPatch = { nodeId: string; displayOrder: number }

function AdminNodeList({ nodes, onEdit, onReorder }: { nodes: AdminNode[]; onEdit: (nodeId: string) => void; onReorder: (nodeId: string, direction: AdminNodeOrderDirection) => void }) {
  const orderedNodes = sortAdminNodes(nodes)
  return (
    <div className="admin-list" role="list" aria-label="服务器列表">
      <div className="admin-list-head" aria-hidden="true">
        <span>服务器</span>
        <span>状态</span>
        <span>公网 IP</span>
        <span>账单</span>
        <span>系统</span>
        <span>最近在线</span>
        <span>Agent</span>
        <span>操作</span>
      </div>
      {orderedNodes.map((node, index) => (
        <article className="admin-list-row" role="listitem" key={node.id}>
          <div className="admin-list-main">
            <strong>{node.displayName}</strong>
            <small>{node.id} · {countryCodeToFlag(node.countryCode)} {formatAdminLocation(node)} · 顺序 {node.displayOrder}</small>
          </div>
          <span className={`admin-node-status status-${node.disabled ? 'disabled' : node.status}`}>{node.disabled ? 'disabled' : node.status}</span>
          <span>{formatAdminPublicIPs(node)}</span>
          <span>{formatAdminBilling(node)}</span>
          <span>{formatAdminSystem(node)}</span>
          <span>{formatAdminDate(node.lastSeenAt)}</span>
          <span>{node.agentVersion || '—'}</span>
          <div className="admin-row-actions">
            <button className="admin-row-action" type="button" onClick={() => onReorder(node.id, 'up')} disabled={index === 0}>上移</button>
            <button className="admin-row-action" type="button" onClick={() => onReorder(node.id, 'down')} disabled={index === orderedNodes.length - 1}>下移</button>
            <button className="admin-row-action" type="button" onClick={() => onEdit(node.id)}>编辑服务器</button>
          </div>
        </article>
      ))}
    </div>
  )
}

function sortAdminNodes(nodes: AdminNode[]): AdminNode[] {
  return [...nodes].sort((left, right) => left.displayOrder - right.displayOrder || left.id.localeCompare(right.id))
}

function buildAdminNodeOrderPatches(nodes: AdminNode[], nodeId?: string, direction?: AdminNodeOrderDirection): AdminNodeOrderPatch[] {
  const orderedNodes = sortAdminNodes(nodes)
  const reorderedNodes = [...orderedNodes]
  if (nodeId && direction) {
    const index = reorderedNodes.findIndex((node) => node.id === nodeId)
    if (index < 0) return []
    const targetIndex = direction === 'up' ? index - 1 : index + 1
    if (targetIndex < 0 || targetIndex >= reorderedNodes.length) return []
    const current = reorderedNodes[index]
    reorderedNodes[index] = reorderedNodes[targetIndex]
    reorderedNodes[targetIndex] = current
  }
  return reorderedNodes
    .map((node, index) => ({ nodeId: node.id, displayOrder: (index + 1) * 10 }))
    .filter((patch) => orderedNodes.find((node) => node.id === patch.nodeId)?.displayOrder !== patch.displayOrder)
}

function AdminNodeCreateModal({ onCreate, onClose }: { onCreate: (input: AdminNodeCreateInput) => void; onClose: () => void }) {
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const formData = new FormData(form)
    const displayName = String(formData.get('new-display-name') ?? '').trim()
    if (displayName === '') return
    onCreate({
      id: String(formData.get('new-node-id') ?? '').trim() || undefined,
      displayName,
      countryCode: String(formData.get('new-country-code') ?? ''),
      region: String(formData.get('new-region') ?? ''),
      expiryDate: String(formData.get('new-expiry-date') ?? '').trim(),
      billingCycle: String(formData.get('new-billing-cycle') ?? '').trim(),
      billingMode: String(formData.get('new-billing-mode') ?? 'both'),
      monthlyResetDay: parseMonthlyResetDay(String(formData.get('new-monthly-reset-day') ?? '')) ?? 1,
      displayOrder: parseNonNegativeInt(String(formData.get('new-display-order') ?? '')) ?? 0,
      publicIPv4: String(formData.get('new-public-ipv4') ?? '').trim(),
      publicIPv6: String(formData.get('new-public-ipv6') ?? '').trim(),
      monthlyQuotaBytes: parseQuotaGigabytes(String(formData.get('new-monthly-quota-gb') ?? '')),
    })
  }

  return (
    <AdminModal title="添加服务器" eyebrow="Servers" onClose={onClose}>
      <form className="admin-node-create-form admin-node-edit-form" aria-label="添加服务器" onSubmit={handleSubmit}>
        <label>
          <span>服务器名称</span>
          <input name="new-display-name" autoComplete="off" placeholder="New Server" />
        </label>
        <label>
          <span>节点 ID（可选）</span>
          <input name="new-node-id" autoComplete="off" placeholder="自动生成" />
        </label>
        <label>
          <span>国家</span>
          <input name="new-country-code" autoComplete="off" placeholder="HK" />
        </label>
        <label>
          <span>地区</span>
          <input name="new-region" autoComplete="off" placeholder="Hong Kong" />
        </label>
        <label>
          <span>到期日</span>
          <input name="new-expiry-date" type="date" autoComplete="off" />
        </label>
        <label>
          <span>账单周期</span>
          <input name="new-billing-cycle" autoComplete="off" placeholder="月付 / 年付" />
        </label>
        <label>
          <span>流量计费口径</span>
          <select name="new-billing-mode" defaultValue="both">
            <option value="both">入站 + 出站</option>
            <option value="in">只算入站</option>
            <option value="out">只算出站</option>
            <option value="max">入/出取较大值</option>
          </select>
        </label>
        <label>
          <span>月流量重置日</span>
          <input name="new-monthly-reset-day" type="number" min="1" max="31" step="1" defaultValue="1" />
        </label>
        <label>
          <span>显示顺序</span>
          <input name="new-display-order" type="number" min="0" step="1" defaultValue="0" />
        </label>
        <label>
          <span>公网 IPv4</span>
          <input name="new-public-ipv4" autoComplete="off" placeholder="198.51.100.8" />
        </label>
        <label>
          <span>公网 IPv6</span>
          <input name="new-public-ipv6" autoComplete="off" placeholder="2001:db8::8" />
        </label>
        <label>
          <span>月配额 GB</span>
          <input name="new-monthly-quota-gb" type="number" min="0" step="0.01" />
        </label>
        <button type="submit">添加服务器</button>
      </form>
    </AdminModal>
  )
}

function AdminNodeEditModal({ node, onUpdate, onInstallCommand, onClose }: { node: AdminNode; onUpdate: (nodeId: string, input: AdminNodeUpdateInput) => void; onInstallCommand: (nodeId: string) => Promise<string>; onClose: () => void }) {
  const [installCommandState, setInstallCommandState] = useState<{ kind: 'idle' } | { kind: 'loading' } | { kind: 'ready'; command: string } | { kind: 'error'; message: string }>({ kind: 'idle' })

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    onUpdate(node.id, {
      displayName: String(formData.get('display-name') ?? ''),
      countryCode: String(formData.get('country-code') ?? ''),
      region: String(formData.get('region') ?? ''),
      expiryDate: String(formData.get('expiry-date') ?? '').trim(),
      billingCycle: String(formData.get('billing-cycle') ?? '').trim(),
      billingMode: String(formData.get('billing-mode') ?? node.billingMode),
      monthlyResetDay: parseMonthlyResetDay(String(formData.get('monthly-reset-day') ?? '')) ?? node.monthlyResetDay,
      displayOrder: parseNonNegativeInt(String(formData.get('display-order') ?? '')) ?? node.displayOrder,
      publicIPv4: String(formData.get('public-ipv4') ?? '').trim(),
      publicIPv6: String(formData.get('public-ipv6') ?? '').trim(),
      monthlyQuotaBytes: parseQuotaGigabytes(String(formData.get('monthly-quota-gb') ?? '')),
      disabled: formData.get('disabled') === 'on',
    })
  }

  const handleInstallCommand = () => {
    setInstallCommandState({ kind: 'loading' })
    onInstallCommand(node.id)
      .then((command) => setInstallCommandState({ kind: 'ready', command }))
      .catch((error: unknown) => setInstallCommandState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  return (
    <AdminModal title={`编辑服务器 · ${node.displayName}`} eyebrow={node.id} onClose={onClose}>
      <dl className="admin-modal-summary">
        <div><dt>状态</dt><dd>{node.disabled ? 'disabled' : node.status}</dd></div>
        <div><dt>账单</dt><dd>{formatAdminBilling(node)}</dd></div>
        <div><dt>公网 IP</dt><dd>{formatAdminPublicIPs(node)}</dd></div>
        <div><dt>顺序</dt><dd>{node.displayOrder}</dd></div>
        <div><dt>系统</dt><dd>{formatAdminSystem(node)}</dd></div>
        <div><dt>资源</dt><dd>{formatAdminResources(node)}</dd></div>
        <div><dt>最近在线</dt><dd>{formatAdminDate(node.lastSeenAt)}</dd></div>
      </dl>
      <form className="admin-node-edit-form" aria-label={`${node.displayName} 节点编辑`} onSubmit={handleSubmit}>
        <label>
          <span>显示名</span>
          <input name="display-name" defaultValue={node.displayName} autoComplete="off" />
        </label>
        <label>
          <span>国家</span>
          <input name="country-code" defaultValue={node.countryCode ?? ''} autoComplete="off" />
        </label>
        <label>
          <span>地区</span>
          <input name="region" defaultValue={node.region ?? ''} autoComplete="off" />
        </label>
        <label>
          <span>到期日</span>
          <input name="expiry-date" type="date" defaultValue={node.expiryDate ?? ''} autoComplete="off" />
        </label>
        <label>
          <span>账单周期</span>
          <input name="billing-cycle" defaultValue={node.billingCycle ?? ''} autoComplete="off" />
        </label>
        <label>
          <span>流量计费口径</span>
          <select name="billing-mode" defaultValue={node.billingMode || 'both'}>
            <option value="both">入站 + 出站</option>
            <option value="in">只算入站</option>
            <option value="out">只算出站</option>
            <option value="max">入/出取较大值</option>
          </select>
        </label>
        <label>
          <span>月流量重置日</span>
          <input name="monthly-reset-day" type="number" min="1" max="31" step="1" defaultValue={node.monthlyResetDay || 1} />
        </label>
        <label>
          <span>显示顺序</span>
          <input name="display-order" type="number" min="0" step="1" defaultValue={node.displayOrder} />
        </label>
        <label>
          <span>公网 IPv4</span>
          <input name="public-ipv4" defaultValue={node.publicIPv4 ?? ''} autoComplete="off" />
        </label>
        <label>
          <span>公网 IPv6</span>
          <input name="public-ipv6" defaultValue={node.publicIPv6 ?? ''} autoComplete="off" />
        </label>
        <label>
          <span>月配额 GB</span>
          <input name="monthly-quota-gb" type="number" min="0" step="0.01" defaultValue={formatQuotaGigabytes(node.monthlyQuotaBytes)} />
        </label>
        <label className="admin-node-toggle">
          <input name="disabled" type="checkbox" defaultChecked={node.disabled} />
          <span>禁用节点</span>
        </label>
        <div className="admin-modal-actions">
          <button type="submit">保存服务器</button>
          <button type="button" onClick={handleInstallCommand} disabled={installCommandState.kind === 'loading'}>{installCommandState.kind === 'loading' ? '生成中…' : '获取安装命令'}</button>
        </div>
      </form>
      {installCommandState.kind === 'ready' && (
        <textarea className="admin-install-command" aria-label={`${node.displayName} Agent 安装命令`} readOnly value={installCommandState.command} />
      )}
      {installCommandState.kind === 'error' && <div className="admin-install-error">安装命令生成失败：{installCommandState.message}</div>}
    </AdminModal>
  )
}

function AdminTargetSection({ targets, nodes, onCreate, onUpdate, onDelete }: { targets: AdminProbeTarget[]; nodes: AdminNode[]; onCreate: (input: AdminProbeTargetInput) => void; onUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => void; onDelete: (targetId: string) => void }) {
  const [creatingTarget, setCreatingTarget] = useState(false)
  const [editingTargetId, setEditingTargetId] = useState<string | null>(null)
  const [targetSort, setTargetSort] = useState<AdminTargetSort>('order')
  const editingTarget = editingTargetId ? targets.find((target) => target.id === editingTargetId) : undefined
  const sortedTargets = sortAdminProbeTargets(targets, targetSort)
  const applyOrderPatches = (patches: AdminProbeTargetOrderPatch[]) => {
    patches.forEach((patch) => onUpdate(patch.targetId, { displayOrder: patch.displayOrder }))
    if (patches.length > 0) setTargetSort('order')
  }

  return (
    <section className="admin-target-section admin-workspace-panel" aria-label="admin probe target list">
      <header className="admin-section-heading">
        <div>
          <p className="eyebrow">Latency</p>
          <h3>延迟监控</h3>
        </div>
        <div className="admin-section-actions">
          <label className="admin-sort-control">
            <span>排序</span>
            <select name="target-sort" value={targetSort} onChange={(event) => setTargetSort(event.currentTarget.value as AdminTargetSort)}>
              <option value="order">按手动顺序</option>
              <option value="name">按名称排序</option>
              <option value="status">按启用状态排序</option>
              <option value="type">按类型排序</option>
              <option value="assignments">按节点分配排序</option>
            </select>
          </label>
          <button type="button" onClick={() => applyOrderPatches(buildAdminProbeTargetOrderPatches(sortedTargets))}>整理顺序</button>
          <button className="admin-primary-action" type="button" onClick={() => setCreatingTarget(true)}>添加目标</button>
        </div>
      </header>

      {targets.length === 0 && <div className="admin-state-card">还没有探针目标。</div>}
      {targets.length > 0 && <AdminTargetList targets={sortedTargets} nodes={nodes} onEdit={setEditingTargetId} onUpdate={onUpdate} onDelete={onDelete} onReorder={(targetId, direction) => applyOrderPatches(buildAdminProbeTargetOrderPatches(sortedTargets, targetId, direction))} />}

      {creatingTarget && (
        <AdminTargetCreateModal
          onClose={() => setCreatingTarget(false)}
          onCreate={(input) => {
            onCreate(input)
            setCreatingTarget(false)
          }}
        />
      )}

      {editingTarget && (
        <AdminTargetEditModal
          key={editingTarget.id}
          target={editingTarget}
          nodes={nodes}
          onClose={() => setEditingTargetId(null)}
          onUpdate={(targetId, input) => {
            onUpdate(targetId, input)
            setEditingTargetId(null)
          }}
        />
      )}
    </section>
  )
}

type AdminProbeTargetOrderDirection = 'up' | 'down'
type AdminProbeTargetOrderPatch = { targetId: string; displayOrder: number }

function AdminTargetList({ targets, nodes, onEdit, onUpdate, onDelete, onReorder }: { targets: AdminProbeTarget[]; nodes: AdminNode[]; onEdit: (targetId: string) => void; onUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => void; onDelete: (targetId: string) => void; onReorder: (targetId: string, direction: AdminProbeTargetOrderDirection) => void }) {
  const confirmDelete = (target: AdminProbeTarget) => {
    const ok = typeof window === 'undefined' ? true : window.confirm(`确认删除延迟监控目标「${target.name}」？`)
    if (ok) onDelete(target.id)
  }
  const updateAllAssignments = (target: AdminProbeTarget, enabled: boolean) => {
    const assignments = targetAssignmentRows(target, nodes).map((assignment) => ({ nodeId: assignment.nodeId, enabled }))
    if (assignments.length > 0) onUpdate(target.id, { assignments })
  }

  return (
    <div className="admin-list admin-target-list" role="list" aria-label="延迟监控目标列表">
      <div className="admin-list-head" aria-hidden="true">
        <span>目标</span>
        <span>状态</span>
        <span>地址</span>
        <span>参数</span>
        <span>节点</span>
        <span>操作</span>
      </div>
      {targets.map((target, index) => (
        <article className="admin-list-row" role="listitem" key={target.id}>
          <div className="admin-list-main">
            <strong>{target.name}</strong>
            <small>{target.id} · 顺序 {target.displayOrder}</small>
          </div>
          <span className={`admin-node-status status-${target.enabled ? 'online' : 'disabled'}`}>{target.enabled ? 'enabled' : 'disabled'}</span>
          <span>{formatTargetEndpoint(target)}</span>
          <span>{formatTargetTypeLabel(target.type)} · {target.count} 次 / {target.timeoutMs}ms / {target.intervalSec}s</span>
          <span>{formatTargetAssignmentSummary(target)}</span>
          <div className="admin-row-actions">
            <button className="admin-row-action" type="button" onClick={() => onReorder(target.id, 'up')} disabled={index === 0}>上移</button>
            <button className="admin-row-action" type="button" onClick={() => onReorder(target.id, 'down')} disabled={index === targets.length - 1}>下移</button>
            <button className="admin-row-action" type="button" onClick={() => onEdit(target.id)}>编辑目标</button>
            <button className="admin-row-action" type="button" onClick={() => onUpdate(target.id, { enabled: !target.enabled })}>
              {target.enabled ? '停用目标' : '启用目标'}
            </button>
            <button className="admin-row-action" type="button" onClick={() => updateAllAssignments(target, true)}>全节点启用</button>
            <button className="admin-row-action" type="button" onClick={() => updateAllAssignments(target, false)}>全节点停用</button>
            <button className="admin-row-action is-danger" type="button" onClick={() => confirmDelete(target)}>删除目标</button>
          </div>
        </article>
      ))}
    </div>
  )
}

function AdminTargetCreateModal({ onCreate, onClose }: { onCreate: (input: AdminProbeTargetInput) => void; onClose: () => void }) {
  const [targetType, setTargetType] = useState<ProbeType>('tcping')

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const name = String(formData.get('new-target-name') ?? '').trim()
    const type = normalizeTargetFormType(String(formData.get('new-target-type') ?? 'tcping'))
    const address = String(formData.get('new-target-address') ?? '').trim()
    const port = type === 'tcping' ? parsePositiveInt(String(formData.get('new-target-port') ?? '')) : null
    if (name === '' || address === '' || (type === 'tcping' && port === null)) return
    onCreate({
      name,
      type,
      address,
      port,
      count: parsePositiveInt(String(formData.get('new-target-count') ?? '')) ?? 3,
      timeoutMs: parsePositiveInt(String(formData.get('new-target-timeout-ms') ?? '')) ?? 1200,
      intervalSec: parsePositiveInt(String(formData.get('new-target-interval-sec') ?? '')) ?? 60,
      displayOrder: parseNonNegativeInt(String(formData.get('new-target-display-order') ?? '')) ?? 0,
    })
  }

  return (
    <AdminModal title="添加延迟监控目标" eyebrow="Latency" onClose={onClose}>
      <form className="admin-target-create-form admin-node-edit-form" aria-label="添加探针目标" onSubmit={handleSubmit}>
        <label>
          <span>目标名称</span>
          <input name="new-target-name" autoComplete="off" placeholder="Example HTTPS" />
        </label>
        <label>
          <span>类型</span>
          <select name="new-target-type" value={targetType} onChange={(event) => setTargetType(normalizeTargetFormType(event.currentTarget.value))}>
            <option value="tcping">TCP Ping</option>
            <option value="ping">ICMP Ping</option>
            <option value="http_get">HTTP GET</option>
          </select>
        </label>
        <label>
          <span>地址</span>
          <input name="new-target-address" autoComplete="off" placeholder="example.com" />
        </label>
        {targetType === 'tcping' && (
          <label>
            <span>端口</span>
            <input name="new-target-port" type="number" min="1" max="65535" defaultValue="443" />
          </label>
        )}
        <label>
          <span>次数</span>
          <input name="new-target-count" type="number" min="1" defaultValue="3" />
        </label>
        <label>
          <span>超时 ms</span>
          <input name="new-target-timeout-ms" type="number" min="1" defaultValue="1200" />
        </label>
        <label>
          <span>间隔 s</span>
          <input name="new-target-interval-sec" type="number" min="1" defaultValue="60" />
        </label>
        <label>
          <span>显示顺序</span>
          <input name="new-target-display-order" type="number" min="0" step="1" defaultValue="0" />
        </label>
        <button type="submit">添加目标</button>
      </form>
    </AdminModal>
  )
}

function AdminTargetEditModal({ target, nodes, onUpdate, onClose }: { target: AdminProbeTarget; nodes: AdminNode[]; onUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => void; onClose: () => void }) {
  const [targetType, setTargetType] = useState<ProbeType>(target.type)
  const assignmentRows = targetAssignmentRows(target, nodes)

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const type = normalizeTargetFormType(String(formData.get('target-type') ?? targetType))
    const port = type === 'tcping' ? parsePositiveInt(String(formData.get('target-port') ?? '')) : null
    if (type === 'tcping' && port === null) return
    onUpdate(target.id, {
      name: String(formData.get('target-name') ?? ''),
      type,
      address: String(formData.get('target-address') ?? ''),
      port,
      count: parsePositiveInt(String(formData.get('target-count') ?? '')) ?? target.count,
      timeoutMs: parsePositiveInt(String(formData.get('target-timeout-ms') ?? '')) ?? target.timeoutMs,
      intervalSec: parsePositiveInt(String(formData.get('target-interval-sec') ?? '')) ?? target.intervalSec,
      displayOrder: parseNonNegativeInt(String(formData.get('target-display-order') ?? '')) ?? target.displayOrder,
      enabled: formData.get('target-enabled') === 'on',
      assignments: assignmentRows.length > 0
        ? assignmentRows.map((assignment) => ({
            nodeId: assignment.nodeId,
            enabled: formData.get(`target-assignment-${assignment.nodeId}`) === 'on',
          }))
        : undefined,
    })
  }

  return (
    <AdminModal title={`编辑延迟监控 · ${target.name}`} eyebrow={target.id} onClose={onClose}>
      <dl className="admin-modal-summary">
        <div><dt>状态</dt><dd>{target.enabled ? 'enabled' : 'disabled'}</dd></div>
        <div><dt>类型</dt><dd>{formatTargetTypeLabel(target.type)}</dd></div>
        <div><dt>地址</dt><dd>{formatTargetEndpoint(target)}</dd></div>
        <div><dt>参数</dt><dd>{target.count} 次 / {target.timeoutMs}ms / {target.intervalSec}s</dd></div>
        <div><dt>顺序</dt><dd>{target.displayOrder}</dd></div>
        <div><dt>节点</dt><dd>{formatTargetAssignmentSummary(target)}</dd></div>
      </dl>
      <form className="admin-target-edit-form admin-node-edit-form" aria-label={`${target.name} 探针目标编辑`} onSubmit={handleSubmit}>
        <label>
          <span>目标名</span>
          <input name="target-name" defaultValue={target.name} autoComplete="off" />
        </label>
        <label>
          <span>类型</span>
          <select name="target-type" value={targetType} onChange={(event) => setTargetType(normalizeTargetFormType(event.currentTarget.value))}>
            <option value="tcping">TCP Ping</option>
            <option value="ping">ICMP Ping</option>
            <option value="http_get">HTTP GET</option>
          </select>
        </label>
        <label>
          <span>地址</span>
          <input name="target-address" defaultValue={target.address} autoComplete="off" />
        </label>
        {targetType === 'tcping' && (
          <label>
            <span>端口</span>
            <input name="target-port" type="number" min="1" max="65535" defaultValue={target.port ?? ''} />
          </label>
        )}
        <label>
          <span>次数</span>
          <input name="target-count" type="number" min="1" defaultValue={target.count} />
        </label>
        <label>
          <span>超时 ms</span>
          <input name="target-timeout-ms" type="number" min="1" defaultValue={target.timeoutMs} />
        </label>
        <label>
          <span>间隔 s</span>
          <input name="target-interval-sec" type="number" min="1" defaultValue={target.intervalSec} />
        </label>
        <label>
          <span>显示顺序</span>
          <input name="target-display-order" type="number" min="0" step="1" defaultValue={target.displayOrder} />
        </label>
        <label className="admin-node-toggle">
          <input name="target-enabled" type="checkbox" defaultChecked={target.enabled} />
          <span>启用目标</span>
        </label>
        {assignmentRows.length > 0 && (
          <fieldset className="admin-target-assignment-list">
            <legend>按节点启用</legend>
            {assignmentRows.map((assignment) => (
              <label className="admin-node-toggle admin-target-assignment-toggle" key={assignment.nodeId}>
                <input name={`target-assignment-${assignment.nodeId}`} type="checkbox" defaultChecked={assignment.enabled} />
                <span>{assignment.nodeDisplayName || assignment.nodeId}</span>
              </label>
            ))}
          </fieldset>
        )}
        <div className="admin-modal-actions">
          <button type="submit">保存目标</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminAlertRulesSection({ rules, states, nodes, onUpdate }: { rules: AdminAlertRule[]; states: AdminAlertRuleState[]; nodes: AdminNode[]; onUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => void }) {
  const [editingRule, setEditingRule] = useState<AdminAlertRule | null>(null)
  const activeStates = states.filter((state) => state.active)

  return (
    <section className="admin-alert-rule-section admin-workspace-panel" aria-label="admin status rules">
      <header className="admin-section-heading">
        <div>
          <p className="eyebrow">Rules</p>
          <h3>状态规则</h3>
          <p>通知规则把资源、探测和在线状态映射到现有通知类型。</p>
        </div>
      </header>

      <section className="admin-notification-block" aria-label="当前异常">
        <h4>当前异常</h4>
        {activeStates.length === 0 && <div className="admin-state-card">当前没有命中的状态规则。</div>}
        {activeStates.length > 0 && <AdminAlertRuleStateList states={activeStates} />}
      </section>

      <section className="admin-notification-block" aria-label="通知规则">
        <h4>通知规则</h4>
        {rules.length === 0 && <div className="admin-state-card">还没有状态规则。</div>}
        {rules.length > 0 && <AdminAlertRuleList rules={rules} nodes={nodes} onEdit={setEditingRule} onUpdate={onUpdate} />}
      </section>

      {editingRule && (
        <AdminAlertRuleEditModal
          rule={editingRule}
          nodes={nodes}
          onClose={() => setEditingRule(null)}
          onUpdate={(ruleId, input) => {
            onUpdate(ruleId, input)
            setEditingRule(null)
          }}
        />
      )}
    </section>
  )
}

function AdminAlertRuleStateList({ states }: { states: AdminAlertRuleState[] }) {
  return (
    <div className="admin-list admin-alert-rule-state-list" role="list" aria-label="当前异常列表">
      <div className="admin-list-head" aria-hidden="true">
        <span>节点</span>
        <span>命中规则</span>
        <span>当前值</span>
        <span>条件</span>
        <span>通知</span>
        <span>最近命中</span>
      </div>
      {states.map((state) => (
        <article className="admin-list-row" role="listitem" key={`${state.nodeId}:${state.ruleId}`}>
          <div className="admin-list-main">
            <strong>{state.nodeName || state.nodeId}</strong>
            <small>{state.nodeId}</small>
          </div>
          <div className="admin-list-main">
            <strong>{state.ruleName}</strong>
            <small>{state.ruleId} · {formatRuleCategory(state.category)} · {state.metric}</small>
          </div>
          <span>{formatAlertRuleStateValue(state)}</span>
          <span className="admin-rule-condition">{formatAlertRuleStateCondition(state)}</span>
          <span>通知：{state.notificationLabel || state.notificationEventType}<small> · {state.notificationEventType}</small></span>
          <span>{state.nodeStatus} · {formatAdminDate(state.lastSeenAt)}</span>
        </article>
      ))}
    </div>
  )
}

function AdminAlertRuleList({ rules, nodes, onEdit, onUpdate }: { rules: AdminAlertRule[]; nodes: AdminNode[]; onEdit: (rule: AdminAlertRule) => void; onUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => void }) {
  return (
    <div className="admin-list admin-alert-rule-list" role="list" aria-label="状态规则列表">
      <div className="admin-list-head" aria-hidden="true">
        <span>规则</span>
        <span>条件</span>
        <span>持续时间</span>
        <span>通知</span>
        <span>范围</span>
        <span>状态</span>
        <span>操作</span>
      </div>
      {rules.map((rule) => (
        <article className="admin-list-row" role="listitem" key={rule.id}>
          <div className="admin-list-main">
            <strong>{rule.name}</strong>
            <small>{rule.id} · {formatRuleCategory(rule.category)}</small>
          </div>
          <span className="admin-rule-condition">{formatAlertRuleCondition(rule)}</span>
          <span>持续 {rule.durationSec}s</span>
          <span>通知：{rule.notificationLabel || rule.notificationEventType}<small> · {rule.notificationEventType}</small></span>
          <span>{formatAlertRuleScope(rule, nodes)}</span>
          <span className={`admin-node-status status-${rule.enabled ? 'online' : 'disabled'}`}>{rule.enabled ? '启用中' : '已停用'}</span>
          <div className="admin-row-actions">
            <button className="admin-row-action" type="button" onClick={() => onEdit(rule)}>编辑规则</button>
            <button className="admin-row-action" type="button" onClick={() => onUpdate(rule.id, { enabled: !rule.enabled })}>
              {rule.enabled ? '停用规则' : '启用规则'}
            </button>
          </div>
        </article>
      ))}
    </div>
  )
}

function AdminAlertRuleEditModal({ rule, nodes, onUpdate, onClose }: { rule: AdminAlertRule; nodes: AdminNode[]; onUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => void; onClose: () => void }) {
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const threshold = Number(String(formData.get('rule-threshold') ?? '').trim())
    const durationSec = Number(String(formData.get('rule-duration-sec') ?? '').trim())
    const scopeNodeIds = nodes.filter((node) => formData.get(`rule-scope-${node.id}`) === 'on').map((node) => node.id)
    if (!Number.isFinite(threshold) || threshold < 0 || !Number.isInteger(durationSec) || durationSec < 0) return
    onUpdate(rule.id, {
      enabled: formData.get('rule-enabled') === 'on',
      threshold,
      durationSec,
      scopeNodeIds,
    })
  }

  return (
    <AdminModal title={`编辑状态规则 · ${rule.name}`} eyebrow={rule.id} onClose={onClose}>
      <dl className="admin-modal-summary">
        <div><dt>条件</dt><dd>{formatAlertRuleCondition(rule)}</dd></div>
        <div><dt>作用范围</dt><dd>{formatAlertRuleScope(rule, nodes)}</dd></div>
        <div><dt>通知类型</dt><dd>{rule.notificationLabel || rule.notificationEventType}</dd></div>
        <div><dt>当前状态</dt><dd>{rule.enabled ? '启用中' : '已停用'}</dd></div>
      </dl>
      <form className="admin-alert-rule-edit-form admin-node-edit-form" aria-label={`${rule.name} 状态规则编辑`} onSubmit={handleSubmit}>
        <label>
          <span>阈值</span>
          <input name="rule-threshold" type="number" min="0" step="0.01" defaultValue={rule.threshold} />
        </label>
        <label>
          <span>持续时间 s</span>
          <input name="rule-duration-sec" type="number" min="0" step="1" defaultValue={rule.durationSec} />
        </label>
        <label className="admin-node-toggle">
          <input name="rule-enabled" type="checkbox" defaultChecked={rule.enabled} />
          <span>启用规则</span>
        </label>
        {nodes.length > 0 && (
          <fieldset className="admin-rule-scope-list admin-target-assignment-list">
            <legend>作用服务器（不选=全部服务器）</legend>
            {nodes.map((node) => (
              <label className="admin-node-toggle admin-target-assignment-toggle" key={node.id}>
                <input name={`rule-scope-${node.id}`} type="checkbox" defaultChecked={rule.scopeNodeIds.includes(node.id)} />
                <span>{node.displayName || node.id}<small> · {node.id}</small></span>
              </label>
            ))}
          </fieldset>
        )}
        <div className="admin-modal-actions">
          <button type="submit">保存状态规则</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminMaintenanceSection({ maintenance, onUpdate, onCleanup }: { maintenance: AdminMaintenance; onUpdate: (input: AdminMaintenanceUpdateInput) => void; onCleanup: (input: AdminMaintenanceCleanupInput) => Promise<AdminMaintenanceCleanup> }) {
  const [cleanupState, setCleanupState] = useState<{ kind: 'idle' } | { kind: 'loading'; label: string } | { kind: 'ready'; cleanup: AdminMaintenanceCleanup } | { kind: 'error'; message: string }>({ kind: 'idle' })
  const settings = maintenance.settings

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const stateRetentionDays = parsePositiveInt(String(formData.get('maintenance-state-retention-days') ?? ''))
    const probeRetentionDays = parsePositiveInt(String(formData.get('maintenance-probe-retention-days') ?? ''))
    const notificationRetentionDays = parsePositiveInt(String(formData.get('maintenance-notification-retention-days') ?? ''))
    if (stateRetentionDays === null || probeRetentionDays === null || notificationRetentionDays === null) return
    onUpdate({
      enabled: formData.get('maintenance-enabled') === 'on',
      stateRetentionDays,
      probeRetentionDays,
      notificationRetentionDays,
    })
  }

  const handleCleanup = (dryRun: boolean) => {
    if (!dryRun) {
      const ok = typeof window === 'undefined' ? true : window.confirm('确认删除超过保留期的状态、探测和通知历史？此操作不可恢复。')
      if (!ok) return
    }
    setCleanupState({ kind: 'loading', label: dryRun ? '正在预览清理…' : '正在清理历史数据…' })
    onCleanup({ dryRun, confirm: !dryRun })
      .then((cleanup) => setCleanupState({ kind: 'ready', cleanup }))
      .catch((error: unknown) => setCleanupState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  return (
    <section className="admin-maintenance-section admin-workspace-panel" aria-label="admin data maintenance">
      <header className="admin-section-heading">
        <div>
          <p className="eyebrow">Maintenance</p>
          <h3>数据维护</h3>
          <p className="admin-overview-note">按保留期清理历史状态采样、探测结果和通知发送记录；清理前可以先预览候选数量。</p>
        </div>
      </header>

      <section className="admin-notification-block" aria-label="维护候选数据">
        <h4>候选数据</h4>
        <div className="admin-action-grid admin-maintenance-stats">
          <MaintenanceStatCard label="状态采样" value={maintenance.candidates.stateSamples} meta={`保留 ${settings.stateRetentionDays} 天`} />
          <MaintenanceStatCard label="探测轮次" value={maintenance.candidates.probeRounds} meta={`保留 ${settings.probeRetentionDays} 天`} />
          <MaintenanceStatCard label="探测明细" value={maintenance.candidates.probeSamples} meta={`随探测轮次清理`} />
          <MaintenanceStatCard label="通知发送" value={maintenance.candidates.notificationDeliveries} meta={`保留 ${settings.notificationRetentionDays} 天`} />
        </div>
      </section>

      <form className="admin-maintenance-form admin-node-edit-form" aria-label="数据维护设置" onSubmit={handleSubmit}>
        <label className="admin-node-toggle">
          <input name="maintenance-enabled" type="checkbox" defaultChecked={settings.enabled} />
          <span>启用数据维护配置</span>
        </label>
        <label>
          <span>状态采样保留天数</span>
          <input name="maintenance-state-retention-days" type="number" min="1" max="3650" step="1" defaultValue={settings.stateRetentionDays} />
        </label>
        <label>
          <span>探测历史保留天数</span>
          <input name="maintenance-probe-retention-days" type="number" min="1" max="3650" step="1" defaultValue={settings.probeRetentionDays} />
        </label>
        <label>
          <span>通知记录保留天数</span>
          <input name="maintenance-notification-retention-days" type="number" min="1" max="3650" step="1" defaultValue={settings.notificationRetentionDays} />
        </label>
        <div className="admin-modal-actions">
          <button type="submit">保存数据维护设置</button>
          <button type="button" onClick={() => handleCleanup(true)} disabled={cleanupState.kind === 'loading'}>预览清理</button>
          <button className="admin-row-action is-danger" type="button" onClick={() => handleCleanup(false)} disabled={cleanupState.kind === 'loading'}>确认清理</button>
        </div>
      </form>

      {settings.updatedAt && <p className="admin-overview-note">维护设置最近更新：{formatAdminDate(settings.updatedAt)}</p>}
      {cleanupState.kind === 'loading' && <div className="admin-state-card">{cleanupState.label}</div>}
      {cleanupState.kind === 'ready' && <div className="admin-state-card">{cleanupState.cleanup.dryRun ? '预览完成' : '清理完成'}：{formatMaintenanceStats(cleanupState.cleanup.deleted)}；剩余候选 {formatMaintenanceStats(cleanupState.cleanup.candidates)}。</div>}
      {cleanupState.kind === 'error' && <div className="admin-state-card is-error">数据维护失败：{cleanupState.message}</div>}
    </section>
  )
}

function MaintenanceStatCard({ label, value, meta }: { label: string; value: number; meta: string }) {
  return (
    <article className="admin-action-card admin-maintenance-stat-card">
      <p>{label}</p>
      <strong>{value.toLocaleString('zh-CN')} 条</strong>
      <span>{meta}</span>
    </article>
  )
}

function AdminNotificationsSection({ channels, types, deliveries, onChannelCreate, onChannelUpdate, onChannelDelete, onChannelTest, onTypeUpdate }: { channels: AdminNotificationChannel[]; types: AdminNotificationType[]; deliveries: AdminNotificationDelivery[]; onChannelCreate: (input: AdminNotificationChannelCreateInput) => void; onChannelUpdate: (channelId: string, input: AdminNotificationChannelUpdateInput) => void; onChannelDelete: (channelId: string) => void; onChannelTest: (channelId: string) => void; onTypeUpdate: (eventType: string, enabled: boolean) => void }) {
  const [creatingChannel, setCreatingChannel] = useState(false)
  const [editingChannel, setEditingChannel] = useState<AdminNotificationChannel | null>(null)

  return (
    <section className="admin-notification-section admin-workspace-panel" aria-label="admin notification settings">
      <header className="admin-section-heading">
        <div>
          <p className="eyebrow">Notify</p>
          <h3>通知</h3>
        </div>
        <button className="admin-primary-action" type="button" onClick={() => setCreatingChannel(true)}>添加通知渠道</button>
      </header>

      <section className="admin-notification-block" aria-label="通知渠道">
        <h4>通知渠道</h4>
        {channels.length === 0 && <div className="admin-state-card">还没有通知渠道。</div>}
        {channels.length > 0 && <AdminNotificationChannelList channels={channels} onUpdate={onChannelUpdate} onDelete={onChannelDelete} onTest={onChannelTest} onEdit={setEditingChannel} />}
      </section>

      <section className="admin-notification-block" aria-label="通知类型">
        <h4>通知类型</h4>
        <div className="admin-notification-type-grid">
          {types.map((notificationType) => (
            <article className="admin-action-card admin-notification-type-card" key={notificationType.eventType}>
              <p>{notificationType.label}</p>
              <strong>{notificationType.eventType}</strong>
              <span className={`admin-node-status status-${notificationType.enabled ? 'online' : 'disabled'}`}>{notificationType.enabled ? '启用中' : '已停用'}</span>
              <button className="admin-row-action" type="button" onClick={() => onTypeUpdate(notificationType.eventType, !notificationType.enabled)}>
                {notificationType.enabled ? '停用通知类型' : '启用通知类型'}
              </button>
            </article>
          ))}
        </div>
      </section>

      <section className="admin-notification-block" aria-label="最近发送">
        <h4>最近发送</h4>
        {deliveries.length === 0 && <div className="admin-state-card">还没有通知发送记录。</div>}
        {deliveries.length > 0 && <AdminNotificationDeliveryList deliveries={deliveries} />}
      </section>

      {creatingChannel && (
        <AdminNotificationChannelCreateModal
          onClose={() => setCreatingChannel(false)}
          onCreate={(input) => {
            onChannelCreate(input)
            setCreatingChannel(false)
          }}
        />
      )}
      {editingChannel && (
        <AdminNotificationChannelEditModal
          channel={editingChannel}
          onClose={() => setEditingChannel(null)}
          onUpdate={(channelId, input) => {
            onChannelUpdate(channelId, input)
            setEditingChannel(null)
          }}
        />
      )}
    </section>
  )
}

function AdminNotificationDeliveryList({ deliveries }: { deliveries: AdminNotificationDelivery[] }) {
  return (
    <div className="admin-list admin-notification-delivery-list" role="list" aria-label="通知发送记录">
      <div className="admin-list-head" aria-hidden="true">
        <span>事件</span>
        <span>节点</span>
        <span>渠道</span>
        <span>状态</span>
        <span>时间</span>
        <span>结果</span>
      </div>
      {deliveries.map((delivery) => (
        <article className="admin-list-row" role="listitem" key={delivery.id}>
          <div className="admin-list-main">
            <strong>{delivery.label}</strong>
            <small>{delivery.eventType}</small>
          </div>
          <span>{delivery.nodeName || delivery.nodeId}</span>
          <span>{delivery.channelName || delivery.channelId}</span>
          <span>{delivery.previousStatus} → {delivery.status}</span>
          <span>{formatAdminDate(delivery.createdAt)}</span>
          <span className={`admin-node-status status-${delivery.success ? 'online' : 'disabled'}`}>{delivery.success ? '发送成功' : '发送失败'}{delivery.error ? ` · ${delivery.error}` : ''}</span>
        </article>
      ))}
    </div>
  )
}

function AdminNotificationChannelList({ channels, onUpdate, onDelete, onTest, onEdit }: { channels: AdminNotificationChannel[]; onUpdate: (channelId: string, input: AdminNotificationChannelUpdateInput) => void; onDelete: (channelId: string) => void; onTest: (channelId: string) => void; onEdit: (channel: AdminNotificationChannel) => void }) {
  const confirmDelete = (channel: AdminNotificationChannel) => {
    const ok = typeof window === 'undefined' ? true : window.confirm(`确认删除通知渠道「${channel.name}」？`)
    if (ok) onDelete(channel.id)
  }

  return (
    <div className="admin-list admin-notification-list" role="list" aria-label="通知渠道列表">
      <div className="admin-list-head" aria-hidden="true">
        <span>渠道</span>
        <span>状态</span>
        <span>Chat ID</span>
        <span>Bot Token</span>
        <span>操作</span>
      </div>
      {channels.map((channel) => (
        <article className="admin-list-row" role="listitem" key={channel.id}>
          <div className="admin-list-main">
            <strong>{channel.name}</strong>
            <small>{channel.id}</small>
          </div>
          <span className={`admin-node-status status-${channel.enabled ? 'online' : 'disabled'}`}>{channel.enabled ? '启用中' : '已停用'}</span>
          <span className="admin-notification-destination">{channel.destination}</span>
          <span>{channel.credentialSet ? '凭据已设置' : '未设置凭据'}</span>
          <div className="admin-row-actions">
            <button className="admin-row-action" type="button" onClick={() => onTest(channel.id)}>测试发送</button>
            <button className="admin-row-action" type="button" onClick={() => onEdit(channel)}>编辑渠道</button>
            <button className="admin-row-action" type="button" onClick={() => onUpdate(channel.id, { enabled: !channel.enabled })}>
              {channel.enabled ? '停用渠道' : '启用渠道'}
            </button>
            <button className="admin-row-action is-danger" type="button" onClick={() => confirmDelete(channel)}>删除渠道</button>
          </div>
        </article>
      ))}
    </div>
  )
}

function AdminNotificationChannelEditModal({ channel, onUpdate, onClose }: { channel: AdminNotificationChannel; onUpdate: (channelId: string, input: AdminNotificationChannelUpdateInput) => void; onClose: () => void }) {
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const name = String(formData.get('channel-name') ?? '').trim()
    const destination = String(formData.get('channel-destination') ?? '').trim()
    const credential = String(formData.get('channel-credential') ?? '').trim()
    if (name === '' || destination === '') return
    onUpdate(channel.id, {
      name,
      destination,
      ...(credential !== '' ? { credential } : {}),
      enabled: formData.get('channel-enabled') === 'on',
    })
  }

  return (
    <AdminModal title="编辑通知渠道" eyebrow="Notify" onClose={onClose}>
      <form className="admin-notification-edit-form admin-node-edit-form" aria-label="编辑通知渠道" onSubmit={handleSubmit}>
        <label>
          <span>渠道名称</span>
          <input name="channel-name" autoComplete="off" defaultValue={channel.name} />
        </label>
        <label>
          <span>Telegram Chat ID</span>
          <input name="channel-destination" autoComplete="off" defaultValue={channel.destination} />
        </label>
        <label>
          <span>Telegram Bot Token</span>
          <input name="channel-credential" type="password" autoComplete="new-password" placeholder={channel.credentialSet ? '留空则保留当前 Bot Token' : '仅写入，不回显'} />
        </label>
        <label className="admin-node-toggle">
          <input name="channel-enabled" type="checkbox" defaultChecked={channel.enabled} />
          <span>启用渠道</span>
        </label>
        <div className="admin-modal-actions">
          <button type="submit">保存通知渠道</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminNotificationChannelCreateModal({ onCreate, onClose }: { onCreate: (input: AdminNotificationChannelCreateInput) => void; onClose: () => void }) {
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const name = String(formData.get('new-channel-name') ?? '').trim()
    const destination = String(formData.get('new-channel-destination') ?? '').trim()
    const credential = String(formData.get('new-channel-credential') ?? '').trim()
    if (name === '' || destination === '' || credential === '') return
    onCreate({
      name,
      destination,
      credential,
      enabled: formData.get('new-channel-enabled') === 'on',
    })
  }

  return (
    <AdminModal title="添加通知渠道" eyebrow="Notify" onClose={onClose}>
      <form className="admin-notification-create-form admin-node-edit-form" aria-label="添加通知渠道" onSubmit={handleSubmit}>
        <label>
          <span>渠道名称</span>
          <input name="new-channel-name" autoComplete="off" placeholder="Zeno Telegram" />
        </label>
        <label>
          <span>Telegram Chat ID</span>
          <input name="new-channel-destination" autoComplete="off" placeholder="7579942307" />
        </label>
        <label>
          <span>Telegram Bot Token</span>
          <input name="new-channel-credential" type="password" autoComplete="new-password" placeholder="仅写入，不回显" />
        </label>
        <label className="admin-node-toggle">
          <input name="new-channel-enabled" type="checkbox" defaultChecked />
          <span>创建后启用渠道</span>
        </label>
        <div className="admin-modal-actions">
          <button type="submit">保存通知渠道</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminModal({ title, eyebrow, onClose, children }: { title: string; eyebrow: string; onClose: () => void; children: ReactNode }) {
  return (
    <div className="admin-modal-backdrop" role="presentation">
      <section className="admin-modal" role="dialog" aria-modal="true" aria-label={title}>
        <header className="admin-modal-header">
          <div>
            <p className="eyebrow">{eyebrow}</p>
            <h3>{title}</h3>
          </div>
          <button className="admin-modal-close" type="button" onClick={onClose} aria-label="关闭弹窗">×</button>
        </header>
        {children}
      </section>
    </div>
  )
}

function sortAdminProbeTargets(targets: AdminProbeTarget[], sort: AdminTargetSort): AdminProbeTarget[] {
  return [...targets].sort((left, right) => {
    const byName = left.name.localeCompare(right.name, 'zh-CN') || left.id.localeCompare(right.id, 'zh-CN')
    if (sort === 'order') {
      return left.displayOrder - right.displayOrder || left.id.localeCompare(right.id, 'zh-CN')
    }
    if (sort === 'status') {
      return Number(right.enabled) - Number(left.enabled) || byName
    }
    if (sort === 'type') {
      return formatTargetTypeLabel(left.type).localeCompare(formatTargetTypeLabel(right.type), 'zh-CN') || byName
    }
    if (sort === 'assignments') {
      const rightEnabled = right.assignments.filter((assignment) => assignment.enabled).length
      const leftEnabled = left.assignments.filter((assignment) => assignment.enabled).length
      return rightEnabled - leftEnabled || byName
    }
    return byName
  })
}

function buildAdminProbeTargetOrderPatches(targets: AdminProbeTarget[], targetId?: string, direction?: AdminProbeTargetOrderDirection): AdminProbeTargetOrderPatch[] {
  const reorderedTargets = [...targets]
  if (targetId && direction) {
    const index = reorderedTargets.findIndex((target) => target.id === targetId)
    if (index < 0) return []
    const targetIndex = direction === 'up' ? index - 1 : index + 1
    if (targetIndex < 0 || targetIndex >= reorderedTargets.length) return []
    const current = reorderedTargets[index]
    reorderedTargets[index] = reorderedTargets[targetIndex]
    reorderedTargets[targetIndex] = current
  }
  return reorderedTargets
    .map((target, index) => ({ targetId: target.id, displayOrder: (index + 1) * 10 }))
    .filter((patch) => targets.find((target) => target.id === patch.targetId)?.displayOrder !== patch.displayOrder)
}

function targetAssignmentRows(target: AdminProbeTarget, nodes: AdminNode[]) {
  const assignmentByNodeID = new Map(target.assignments.map((assignment) => [assignment.nodeId, assignment]))
  const nodeAssignmentRows = nodes.map((node) => {
    const assignment = assignmentByNodeID.get(node.id)
    return {
      nodeId: node.id,
      nodeDisplayName: node.displayName,
      enabled: assignment?.enabled ?? false,
    }
  })
  const staleAssignmentRows = target.assignments
    .filter((assignment) => !nodes.some((node) => node.id === assignment.nodeId))
    .map((assignment) => ({
      nodeId: assignment.nodeId,
      nodeDisplayName: assignment.nodeDisplayName || assignment.nodeId,
      enabled: assignment.enabled,
    }))
  return [...nodeAssignmentRows, ...staleAssignmentRows]
}

function formatTargetTypeLabel(type: ProbeType): string {
  if (type === 'ping') return 'ICMP Ping'
  if (type === 'http_get') return 'HTTP GET'
  return 'TCP Ping'
}

function normalizeTargetFormType(value: string): ProbeType {
  if (value === 'ping' || value === 'icmp') return 'ping'
  if (value === 'http_get' || value === 'http' || value === 'https') return 'http_get'
  return 'tcping'
}

function formatTargetEndpoint(target: AdminProbeTarget): string {
  return target.port ? `${target.address}:${target.port}` : target.address
}

function formatTargetAssignmentSummary(target: AdminProbeTarget): string {
  if (target.assignments.length === 0) return '未分配节点'
  const enabled = target.assignments.filter((assignment) => assignment.enabled).length
  return `${enabled} / ${target.assignments.length} 节点启用`
}

function formatAlertRuleCondition(rule: AdminAlertRule): string {
  return `${rule.metric} ${rule.comparator} ${formatAlertRuleThreshold(rule)}`
}

function formatAlertRuleThreshold(rule: AdminAlertRule): string {
  const threshold = Number.isInteger(rule.threshold) ? rule.threshold.toFixed(0) : String(rule.threshold)
  return `${threshold}${rule.thresholdUnit}`
}

function formatAlertRuleScope(rule: AdminAlertRule, nodes: AdminNode[]): string {
  if (rule.scopeNodeIds.length === 0) return '全部服务器'
  const labels = rule.scopeNodeIds.map((nodeId) => {
    const node = nodes.find((candidate) => candidate.id === nodeId)
    return node?.displayName ? `${node.displayName} (${nodeId})` : nodeId
  })
  return labels.join('、')
}

function formatAlertRuleStateCondition(state: AdminAlertRuleState): string {
  const threshold = Number.isInteger(state.threshold) ? state.threshold.toFixed(0) : String(state.threshold)
  return `${state.metric} ${state.comparator} ${threshold}${state.thresholdUnit}`
}

function formatAlertRuleStateValue(state: AdminAlertRuleState): string {
  if (state.lastValue === null || !Number.isFinite(state.lastValue)) return '当前值 —'
  const value = Number.isInteger(state.lastValue) ? state.lastValue.toFixed(0) : String(state.lastValue)
  return `当前值 ${value}${state.thresholdUnit}`
}

function formatRuleCategory(category: string): string {
  if (category === 'resource') return '资源'
  if (category === 'probe') return '探测'
  if (category === 'liveness') return '在线状态'
  return category || '规则'
}

function totalMaintenanceCandidates(stats: AdminMaintenanceStats): number {
  return stats.stateSamples + stats.probeRounds + stats.probeSamples + stats.notificationDeliveries
}

function formatMaintenanceStats(stats: AdminMaintenanceStats): string {
  return `状态 ${stats.stateSamples}、探测轮次 ${stats.probeRounds}、探测明细 ${stats.probeSamples}、通知 ${stats.notificationDeliveries}`
}

function parsePositiveInt(value: string): number | null {
  const parsed = Number(value.trim())
  if (!Number.isInteger(parsed) || parsed <= 0) return null
  return parsed
}

function parseNonNegativeInt(value: string): number | null {
  const trimmed = value.trim()
  if (trimmed === '') return null
  const parsed = Number(trimmed)
  if (!Number.isInteger(parsed) || parsed < 0) return null
  return parsed
}

function parseMonthlyResetDay(value: string): number | null {
  const parsed = parseNonNegativeInt(value)
  if (!parsed || parsed < 1 || parsed > 31) return null
  return parsed
}

function formatQuotaGigabytes(value: number | null): string {
  if (!value || value <= 0) return ''
  const gigabytes = value / (1024 ** 3)
  return String(Math.round(gigabytes * 100) / 100)
}

function parseQuotaGigabytes(value: string): number | null {
  const trimmed = value.trim()
  if (trimmed === '') return null
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed) || parsed < 0) return null
  return Math.round(parsed * (1024 ** 3))
}

function formatAdminLocation(node: AdminNode): string {
  return [node.countryCode, node.region].filter(Boolean).join(' · ') || '—'
}

function formatAdminPublicIPs(node: AdminNode): string {
  return [node.publicIPv4, node.publicIPv6].filter(Boolean).join(' / ') || '—'
}

function formatAdminBilling(node: AdminNode): string {
  const details = [node.expiryDate, node.billingCycle, billingModeLabel(node.billingMode), `每月 ${node.monthlyResetDay || 1} 日重置`].filter(Boolean)
  return details.join(' · ') || '—'
}

function billingModeLabel(mode?: string): string {
  switch (mode) {
    case 'in':
      return '只算入站'
    case 'out':
      return '只算出站'
    case 'max':
      return '入/出取较大'
    default:
      return '入站+出站'
  }
}

function countryCodeToFlag(countryCode?: string): string {
  const normalized = countryCode?.trim().toUpperCase()
  if (!normalized || normalized.length !== 2 || !/^[A-Z]{2}$/.test(normalized)) return '🏳️'
  const base = 127397
  return normalized
    .split('')
    .map((char) => String.fromCodePoint(char.charCodeAt(0) + base))
    .join('')
}

function formatAdminSystem(node: AdminNode): string {
  const system = [node.osName, node.osVersion].filter(Boolean).join(' ')
  return system || node.arch || '—'
}

function formatAdminResources(node: AdminNode): string {
  const cpu = node.cpuCores ? `${node.cpuCores}C` : '—'
  const mem = node.memoryTotalBytes ? compactBytes(node.memoryTotalBytes) : '—'
  const disk = node.diskTotalBytes ? compactBytes(node.diskTotalBytes) : '—'
  return `${cpu} / ${mem} / ${disk}`
}

function formatAdminDate(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

export function HomeOverviewPanel({ settings = defaultSettings, totalCount, onlineCount, offlineCount, totalUp, totalDown, upSpeed, downSpeed }: HomeOverviewPanelProps) {
  const onlineRatio = totalCount > 0 ? Math.round((onlineCount / totalCount) * 100) : 0
  const nonOnlineCount = Math.max(totalCount - onlineCount, offlineCount, 0)
  const healthLabel = totalCount === 0 ? '等待接入' : nonOnlineCount === 0 ? '全部在线' : `${nonOnlineCount} 台未在线`
  const healthTone = totalCount > 0 && nonOnlineCount === 0 ? 'is-good' : totalCount > 0 ? 'is-warning' : ''

  return (
    <section className="home-summary" aria-label="server overview">
      <div className="home-summary__intro">
        <div>
          <p className="eyebrow">Zeno Overview</p>
          <h1>服务器运行概览</h1>
          {settings.siteSubtitle && settings.siteSubtitle !== '服务器运行概览' && <p className="home-summary__subtitle">{settings.siteSubtitle}</p>}
        </div>
        <span className={`home-health-pill ${healthTone}`}>{healthLabel}</span>
      </div>

      <div className="home-summary__compact">
        <div className="home-health-block">
          <div className="home-health-number">
            <strong>{onlineCount}</strong>
            <span>/ {totalCount} 在线</span>
          </div>
          <div className="home-health-bar" aria-label={`在线率 ${onlineRatio}%`}>
            <span style={{ transform: `scaleX(${onlineRatio / 100})` }} />
          </div>
          <p>{healthLabel} · 在线率 {onlineRatio}%</p>
        </div>

        <dl className="home-network-grid" aria-label="traffic totals and speeds">
          <div>
            <dt>累计上传</dt>
            <dd>{compactBytes(totalUp)}</dd>
          </div>
          <div>
            <dt>累计下载</dt>
            <dd>{compactBytes(totalDown)}</dd>
          </div>
          <div>
            <dt>实时上传</dt>
            <dd><CircleArrowIcon direction="up" />{compactRate(upSpeed)}</dd>
          </div>
          <div>
            <dt>实时下载</dt>
            <dd><CircleArrowIcon direction="down" />{compactRate(downSpeed)}</dd>
          </div>
        </dl>
      </div>
    </section>
  )
}

function MapIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M14.106 5.553a2 2 0 0 0 1.788 0l3.659-1.83A1 1 0 0 1 21 4.619v12.764a1 1 0 0 1-.553.894l-4.553 2.277a2 2 0 0 1-1.788 0l-4.212-2.106a2 2 0 0 0-1.788 0l-3.659 1.83A1 1 0 0 1 3 19.381V6.618a1 1 0 0 1 .553-.894l4.553-2.277a2 2 0 0 1 1.788 0z" />
      <path d="M15 5.764v15" />
      <path d="M9 3.236v15" />
    </svg>
  )
}

function SunIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41" />
    </svg>
  )
}

function ImageMinusIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M21 9v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h7" />
      <path d="M16 5h6" />
      <circle cx="9" cy="9" r="2" />
      <path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21" />
    </svg>
  )
}

function CircleArrowIcon({ direction }: { direction: 'up' | 'down' }) {
  return direction === 'up' ? (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path fillRule="evenodd" d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Zm-.75-4.75a.75.75 0 0 0 1.5 0V8.66l1.95 2.1a.75.75 0 1 0 1.1-1.02l-3.25-3.5a.75.75 0 0 0-1.1 0L6.2 9.74a.75.75 0 1 0 1.1 1.02l1.95-2.1v4.59Z" clipRule="evenodd" />
    </svg>
  ) : (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path fillRule="evenodd" d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Zm.75-11.25a.75.75 0 0 0-1.5 0v4.59L7.3 9.24a.75.75 0 0 0-1.1 1.02l3.25 3.5a.75.75 0 0 0 1.1 0l3.25-3.5a.75.75 0 1 0-1.1-1.02l-1.95 2.1V6.75Z" clipRule="evenodd" />
    </svg>
  )
}
