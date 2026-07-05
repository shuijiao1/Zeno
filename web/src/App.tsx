import { type CSSProperties, type DragEvent, type FormEvent, type ReactNode, useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { createAdminNode, createAdminNotificationChannel, createAdminProbeTarget, deleteAdminNode, deleteAdminNotificationChannel, deleteAdminProbeTarget, fetchAdminAccount, fetchAdminAlertRules, fetchAdminNodes, fetchAdminNotificationChannels, fetchAdminProbeTargets, fetchAdminSettings, fetchPublicSettings, subscribeNodeLatency, subscribeNodeState, subscribeServiceLatency, subscribeSummary, loginAdmin, logoutAdmin, requestAdminNodeInstallCommand, testAdminNotificationChannel, updateAdminAccount, updateAdminAlertRule, updateAdminNode, updateAdminNotificationChannel, updateAdminNotificationType, updateAdminProbeTarget, updateAdminSettings, type AdminAccountData, type AdminAlertRuleUpdateInput, type AdminNodeCreateInput, type AdminNodeUpdateInput, type AdminNotificationChannelCreateInput, type AdminNotificationChannelUpdateInput, type AdminProbeTargetInput, type AdminProbeTargetUpdateInput, type AdminSettingsUpdateInput, type NodeLatencyData, type NodeStateData, type ServiceLatencyData, type SummaryData } from './api/client'
import { LatencyDetail } from './components/LatencyDetail'
import { LatencyChart } from './components/LatencyChart'
import { ServerCard } from './components/ServerCard'
import { startLiveRefresh } from './lib/liveRefresh'
import { nodePath, parseDashboardRoute, servicePath, type DashboardRoute } from './lib/route'
import type { AdminAlertRule, AdminNode, AdminNotificationChannel, AdminProbeTarget, AdminSettings, AdminTheme, LatencyPoint, ProbeType, ServiceTarget } from './types'

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

type ServiceLatencyLoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; data: ServiceLatencyData }
  | { kind: 'error'; message: string }

type AdminLoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; account: AdminAccountData; nodes: AdminNode[]; targets: AdminProbeTarget[]; notificationChannels: AdminNotificationChannel[]; alertRules: AdminAlertRule[] }
  | { kind: 'error'; message: string }

type AdminAuthState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'error'; message: string }

type AdminSection = 'nodes' | 'targets' | 'notifications' | 'account' | 'settings'

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
  agentControllerUrl: '',
  backgroundUrl: '',
  desktopBackgroundUrl: '',
  mobileBackgroundUrl: '',
}

function backgroundImageValue(url: string): string {
  return `url("${url.replaceAll('"', '%22')}")`
}

function storedThemeOverride(): AdminTheme | null {
  if (typeof window === 'undefined') return null
  const value = window.localStorage.getItem('zeno_theme_override')
  return value === 'light' || value === 'dark' ? value : null
}

function storedBackgroundEnabled(): boolean {
  if (typeof window === 'undefined') return true
  return window.localStorage.getItem('zeno_background_enabled') !== 'false'
}

function systemTheme(): Exclude<AdminTheme, 'system'> {
  if (typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: dark)').matches) return 'dark'
  return 'light'
}

function resolvedTheme(theme: AdminTheme): Exclude<AdminTheme, 'system'> {
  return theme === 'system' ? systemTheme() : theme
}

function settingsForChrome(settings: AdminSettings, themeOverride: AdminTheme | null, backgroundEnabled: boolean): AdminSettings {
  const nextSettings = { ...settings, theme: themeOverride ?? settings.theme }
  if (backgroundEnabled) return nextSettings
  return { ...nextSettings, backgroundUrl: '', desktopBackgroundUrl: '', mobileBackgroundUrl: '' }
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

function alertRuleAppliesToNode(rule: AdminAlertRule, nodeId: string): boolean {
  return rule.scopeNodeIds.length === 0 || rule.scopeNodeIds.includes(nodeId)
}

export function App() {
  const [state, setState] = useState<LoadState>({ kind: 'loading' })
  const [route, setRoute] = useState<DashboardRoute>(() => parseDashboardRoute(window.location.pathname))
  const [latencyRange, setLatencyRange] = useState('1h')
  const [stateRange, setStateRange] = useState('1h')
  const [latencyState, setLatencyState] = useState<LatencyLoadState>({ kind: 'idle' })
  const [stateHistoryState, setStateHistoryState] = useState<StateHistoryLoadState>({ kind: 'idle' })
  const [serviceLatencyState, setServiceLatencyState] = useState<ServiceLatencyLoadState>({ kind: 'idle' })
  const [adminToken, setAdminToken] = useState(() => window.sessionStorage.getItem('zeno_admin_token') ?? '')
  const [adminAuthState, setAdminAuthState] = useState<AdminAuthState>({ kind: 'idle' })
  const [adminState, setAdminState] = useState<AdminLoadState>({ kind: 'idle' })
  const [settings, setSettings] = useState<AdminSettings>(defaultSettings)
  const [themeOverride, setThemeOverride] = useState<AdminTheme | null>(() => storedThemeOverride())
  const [backgroundEnabled, setBackgroundEnabled] = useState(() => storedBackgroundEnabled())

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
    const stopSummaryStream = subscribeSummary(
      (data) => {
        if (!cancelled) setState({ kind: 'ready', data })
      },
      (error) => {
        if (!cancelled) setState({ kind: 'error', message: error.message })
      },
    )
    if (!stopSummaryStream) setState({ kind: 'error', message: 'websocket unsupported' })
    return () => {
      cancelled = true
      stopSummaryStream?.()
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
    setLatencyState({ kind: 'loading' })
    const stopLatencyStream = subscribeNodeLatency(
      route.nodeId,
      latencyRange,
      (data) => {
        if (!cancelled) setLatencyState({ kind: 'ready', data })
      },
      (error) => {
        if (!cancelled) setLatencyState({ kind: 'error', message: error.message })
      },
    )
    if (!stopLatencyStream) setLatencyState({ kind: 'error', message: 'websocket unsupported' })
    return () => {
      cancelled = true
      stopLatencyStream?.()
    }
  }, [route, latencyRange])

  useEffect(() => {
    if (route.kind !== 'node') {
      setStateHistoryState({ kind: 'idle' })
      return
    }

    let cancelled = false
    setStateHistoryState({ kind: 'loading' })
    const stopStateStream = subscribeNodeState(
      route.nodeId,
      stateRange,
      (data) => {
        if (!cancelled) setStateHistoryState({ kind: 'ready', data })
      },
      (error) => {
        if (!cancelled) setStateHistoryState({ kind: 'error', message: error.message })
      },
    )
    if (!stopStateStream) setStateHistoryState({ kind: 'error', message: 'websocket unsupported' })
    return () => {
      cancelled = true
      stopStateStream?.()
    }
  }, [route, stateRange])

  useEffect(() => {
    if (route.kind !== 'service') {
      setServiceLatencyState({ kind: 'idle' })
      return
    }

    let cancelled = false
    setServiceLatencyState({ kind: 'loading' })
    const stopServiceLatencyStream = subscribeServiceLatency(
      route.targetId,
      latencyRange,
      (data) => {
        if (!cancelled) setServiceLatencyState({ kind: 'ready', data })
      },
      (error) => {
        if (!cancelled) setServiceLatencyState({ kind: 'error', message: error.message })
      },
    )
    if (!stopServiceLatencyStream) setServiceLatencyState({ kind: 'error', message: 'websocket unsupported' })
    return () => {
      cancelled = true
      stopServiceLatencyStream?.()
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
      Promise.all([fetchAdminSettings(adminToken), fetchAdminAccount(adminToken), fetchAdminNodes(adminToken), fetchAdminProbeTargets(adminToken), fetchAdminNotificationChannels(adminToken), fetchAdminAlertRules(adminToken)])
        .then(([settingsData, accountData, nodesData, targetsData, channelsData, alertRulesData]) => {
          loadedOnce = true
          if (!cancelled) {
            setSettings(settingsData)
            setAdminState({ kind: 'ready', account: accountData, nodes: nodesData.nodes, targets: targetsData.targets, notificationChannels: channelsData.channels, alertRules: alertRulesData.rules })
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

  const submitAdminLogin = (username: string, password: string) => {
    const trimmedUsername = username.trim()
    const trimmedPassword = password.trim()
    if (trimmedUsername === '' || trimmedPassword === '') return
    setAdminAuthState({ kind: 'loading' })
    loginAdmin(trimmedUsername, trimmedPassword)
      .then((session) => {
        window.sessionStorage.setItem('zeno_admin_token', session.token)
        setAdminToken(session.token)
        setAdminAuthState({ kind: 'idle' })
      })
      .catch((error: unknown) => {
        setAdminAuthState({ kind: 'error', message: error instanceof Error ? error.message : '登录失败' })
      })
  }

  const clearAdminToken = () => {
    if (adminToken !== '') {
      logoutAdmin(adminToken).catch(() => {})
    }
    window.sessionStorage.removeItem('zeno_admin_token')
    setAdminToken('')
    setAdminAuthState({ kind: 'idle' })
    setAdminState({ kind: 'idle' })
  }

  const updateAdminAccountDetails = (username: string, currentPassword: string, newPassword: string): Promise<void> => {
    if (adminToken === '') return Promise.reject(new Error('missing admin token'))
    return updateAdminAccount(adminToken, username, currentPassword, newPassword).then((session) => {
      window.sessionStorage.setItem('zeno_admin_token', session.token)
      setAdminToken(session.token)
      setAdminState((current) => current.kind === 'ready' ? { ...current, account: { username: session.username } } : current)
    })
  }

  const createAdminNodeDetails = (input: AdminNodeCreateInput): Promise<AdminNode> => {
    if (adminToken === '') return Promise.reject(new Error('missing admin token'))
    return createAdminNode(adminToken, input)
      .then((createdNode) => {
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return { ...current, nodes: sortAdminNodes([...current.nodes, createdNode]) }
          }
          return { kind: 'ready', account: { username: 'admin' }, nodes: [createdNode], targets: [], notificationChannels: [], alertRules: [] }
        })
        return createdNode
      })
      .catch((error: unknown) => {
        setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
        throw error
      })
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
            }
          }
          return { kind: 'ready', account: { username: 'admin' }, nodes: [updatedNode], targets: [], notificationChannels: [], alertRules: [] }
        })
      })
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const deleteAdminNodeDetails = (nodeId: string) => {
    if (adminToken === '') return
    deleteAdminNode(adminToken, nodeId)
      .then(() => {
        setAdminState((current) => {
          if (current.kind !== 'ready') return current
          return {
            ...current,
            nodes: current.nodes.filter((node) => node.id !== nodeId),
            targets: current.targets.map((target) => ({
              ...target,
              assignments: target.assignments.filter((assignment) => assignment.nodeId !== nodeId),
            })),
          }
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
            return { ...current, targets: sortAdminProbeTargets([...current.targets, createdTarget]) }
          }
          return { kind: 'ready', account: { username: 'admin' }, nodes: [], targets: [createdTarget], notificationChannels: [], alertRules: [] }
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
            return { ...current, targets: sortAdminProbeTargets(current.targets.map((target) => target.id === updatedTarget.id ? updatedTarget : target)) }
          }
          return { kind: 'ready', account: { username: 'admin' }, nodes: [], targets: [updatedTarget], notificationChannels: [], alertRules: [] }
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
          return { kind: 'ready', account: { username: 'admin' }, nodes: [], targets: [], notificationChannels: [createdChannel], alertRules: [] }
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
          return { kind: 'ready', account: { username: 'admin' }, nodes: [], targets: [], notificationChannels: [updatedChannel], alertRules: [] }
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
      .then(() => {})
      .catch((error: unknown) => setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const updateAdminAlertRuleDetails = (ruleId: string, input: AdminAlertRuleUpdateInput) => {
    if (adminToken === '') return
    updateAdminAlertRule(adminToken, ruleId, input)
      .then(async (updatedRule) => {
        if (input.enabled === true) {
          await updateAdminNotificationType(adminToken, updatedRule.notificationEventType, true)
        }
        setAdminState((current) => {
          if (current.kind === 'ready') {
            return {
              ...current,
              alertRules: current.alertRules.map((rule) => rule.id === updatedRule.id ? updatedRule : rule),
            }
          }
          return { kind: 'ready', account: { username: 'admin' }, nodes: [], targets: [], notificationChannels: [], alertRules: [updatedRule] }
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
    setStateRange('1h')
    setRoute({ kind: 'node', nodeId })
  }

  const navigateService = (targetId: string) => {
    window.history.pushState(null, '', servicePath(targetId))
    setLatencyRange('1d')
    setRoute({ kind: 'service', targetId })
  }

  const toggleTheme = () => {
    setThemeOverride((current) => {
      const currentTheme = resolvedTheme(current ?? settings.theme)
      const nextTheme: AdminTheme = currentTheme === 'dark' ? 'light' : 'dark'
      window.localStorage.setItem('zeno_theme_override', nextTheme)
      return nextTheme
    })
  }

  const toggleBackground = () => {
    setBackgroundEnabled((current) => {
      const nextValue = !current
      window.localStorage.setItem('zeno_background_enabled', String(nextValue))
      return nextValue
    })
  }

  const effectiveSettings = settingsForChrome(settings, themeOverride, backgroundEnabled)
  const nodes = state.kind === 'ready' ? state.data.nodes : []
  const services = state.kind === 'ready' ? state.data.services : []
  const selectedNode = route.kind === 'node' ? nodes.find((node) => node.id === route.nodeId) : undefined
  const selectedService = route.kind === 'service' ? services.find((service) => service.id === route.targetId) : undefined
  const totalCount = nodes.length
  const onlineCount = nodes.filter((node) => node.status === 'online').length
  const offlineCount = nodes.filter((node) => node.status === 'offline').length
  const totalUp = sum(nodes.map((node) => node.netOutTotalBytes))
  const totalDown = sum(nodes.map((node) => node.netInTotalBytes))
  const upSpeed = sum(nodes.map((node) => node.netOutSpeedBps))
  const downSpeed = sum(nodes.map((node) => node.netInSpeedBps))

  return (
    <main className="kulin-shell" data-theme={effectiveSettings.theme} style={shellStyleForSettings(effectiveSettings)}>
      {(route.kind === 'node' || route.kind === 'service') && <DashboardHeader settings={effectiveSettings} onHome={navigateHome} onAdmin={navigateAdmin} onThemeToggle={toggleTheme} onBackgroundToggle={toggleBackground} backgroundEnabled={backgroundEnabled} />}

      {route.kind === 'admin' && (
        <AdminDashboard
          onHome={navigateHome}
          settings={settings}
          chromeSettings={effectiveSettings}
          hasAdminToken={adminToken !== ''}
          authState={adminAuthState}
          adminState={adminState}
          onAdminLogin={submitAdminLogin}
          onAdminTokenClear={clearAdminToken}
          onAdminAccountUpdate={updateAdminAccountDetails}
          onAdminNodeCreate={createAdminNodeDetails}
          onAdminNodeUpdate={updateAdminNodeDetails}
          onAdminNodeDelete={deleteAdminNodeDetails}
          onAdminInstallCommand={requestAdminInstallCommand}
          onAdminProbeTargetCreate={createAdminProbeTargetDetails}
          onAdminProbeTargetUpdate={updateAdminProbeTargetDetails}
          onAdminProbeTargetDelete={deleteAdminProbeTargetDetails}
          onAdminNotificationChannelCreate={createAdminNotificationChannelDetails}
          onAdminNotificationChannelUpdate={updateAdminNotificationChannelDetails}
          onAdminNotificationChannelDelete={deleteAdminNotificationChannelDetails}
          onAdminNotificationChannelTest={testAdminNotificationChannelDetails}
          onAdminAlertRuleUpdate={updateAdminAlertRuleDetails}
          onAdminSettingsUpdate={updateAdminSettingsDetails}
          onThemeToggle={toggleTheme}
          onBackgroundToggle={toggleBackground}
          backgroundEnabled={backgroundEnabled}
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
          stateRange={stateRange}
          loading={latencyState.kind === 'loading'}
          error={latencyState.kind === 'error' ? latencyState.message : undefined}
          stateLoading={stateHistoryState.kind === 'loading'}
          stateError={stateHistoryState.kind === 'error' ? stateHistoryState.message : undefined}
          onBack={navigateHome}
          onRangeChange={setLatencyRange}
          onStateRangeChange={setStateRange}
        />
      )}

      {state.kind === 'ready' && route.kind === 'node' && !selectedNode && (
        <section className="state-panel is-error">没有找到这台服务器：{route.nodeId}</section>
      )}

      {state.kind === 'ready' && route.kind === 'service' && (selectedService || serviceLatencyState.kind === 'ready') && (
        <ServiceDetail
          target={serviceLatencyState.kind === 'ready' ? serviceLatencyState.data.target : selectedService!}
          points={serviceLatencyState.kind === 'ready' ? serviceLatencyState.data.points : []}
          range={latencyRange}
          loading={serviceLatencyState.kind === 'loading'}
          error={serviceLatencyState.kind === 'error' ? serviceLatencyState.message : undefined}
          onBack={navigateHome}
          onRangeChange={setLatencyRange}
        />
      )}

      {state.kind === 'ready' && route.kind === 'service' && !selectedService && serviceLatencyState.kind === 'error' && (
        <section className="state-panel is-error">没有找到这个监控服务：{route.targetId}</section>
      )}

      {state.kind === 'ready' && route.kind === 'home' && (
        <div className="kulin-container">
          <HomeTopPanel
            settings={effectiveSettings}
            totalCount={totalCount}
            onlineCount={onlineCount}
            offlineCount={offlineCount}
            totalUp={totalUp}
            totalDown={totalDown}
            upSpeed={upSpeed}
            downSpeed={downSpeed}
            onHome={navigateHome}
            onAdmin={navigateAdmin}
            onThemeToggle={toggleTheme}
            onBackgroundToggle={toggleBackground}
            backgroundEnabled={backgroundEnabled}
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
  trailingAction?: ReactNode
  onThemeToggle?: () => void
  onBackgroundToggle?: () => void
  backgroundEnabled?: boolean
}

interface HomeTopPanelProps extends HomeOverviewPanelProps {
  onHome: () => void
  onAdmin: () => void
  onThemeToggle?: () => void
  onBackgroundToggle?: () => void
  backgroundEnabled?: boolean
}

export function HomeTopPanel({ settings = defaultSettings, onHome, onAdmin, onThemeToggle, onBackgroundToggle, backgroundEnabled = true, ...overview }: HomeTopPanelProps) {
  return (
    <section className="home-top-card" aria-label="homepage control panel">
      <DashboardHeader settings={settings} onHome={onHome} onAdmin={onAdmin} onThemeToggle={onThemeToggle} onBackgroundToggle={onBackgroundToggle} backgroundEnabled={backgroundEnabled} />
      <HomeOverviewPanel settings={settings} {...overview} />
    </section>
  )
}

function ServiceDetail({ target, points, range, loading, error, onBack, onRangeChange }: { target: ServiceTarget; points: LatencyPoint[]; range: string; loading?: boolean; error?: string; onBack: () => void; onRangeChange: (range: string) => void }) {
  const [peakCut, setPeakCut] = useState(false)
  const rangeLabel = detailRangeOptions.find((option) => option.value === range)?.label ?? range
  return (
    <div className="kulin-container detail-container">
      <section className="detail-hero" aria-label={`${target.name} service overview`}>
        <div className="detail-hero__main">
          <button className="detail-title-button" type="button" onClick={onBack}>
            <span aria-hidden="true">‹</span>
            <span>{target.name}</span>
          </button>
          <span className={`detail-status-pill status-${serviceTone(target)}`}>{target.reportingNodeCount} / {target.assignedNodeCount} 节点上报</span>
        </div>
        <section className="detail-fact-strip" aria-label={`${target.name} service facts`}>
          <ServiceInfoFact label="类型" value={target.type} />
          <ServiceInfoFact label="地址" value={formatServiceEndpoint(target)} wide />
          <ServiceInfoFact label="最新延迟" value={formatServiceLatency(target.medianMs)} />
          <ServiceInfoFact label="丢包" value={formatServiceLoss(target.lossPercent)} />
          <ServiceInfoFact label="更新时间" value={target.updatedAt ? formatAdminDate(target.updatedAt) : '--'} />
        </section>
      </section>

      <section className="monitor-panel" aria-label={`${target.name} service latency`}>
        <header className="monitor-heading">
          <div>
            <h3>{target.name} 多节点历史</h3>
            <p>{rangeLabel} · 按节点分线展示</p>
          </div>
          <div className="monitor-heading-actions">
            <div className="detail-range-row" aria-label="service latency range selector">
              {detailRangeOptions.map((option) => (
                <button key={option.value} type="button" className={range === option.value ? 'is-active' : ''} onClick={() => onRangeChange(option.value)}>{option.label}</button>
              ))}
            </div>
            <label className="peak-switch">
              <input type="checkbox" aria-label="削峰" checked={peakCut} onChange={(event) => setPeakCut(event.target.checked)} />
              <span />
              <b>削峰</b>
            </label>
          </div>
        </header>
        {loading && <div className="detail-state">正在读取服务延迟…</div>}
        {error && <div className="detail-state is-error">服务延迟读取失败：{error}</div>}
        {!loading && !error && points.length === 0 && <div className="detail-state">暂无服务延迟历史</div>}
        {!loading && !error && points.length > 0 && (
          <LatencyChart points={points} title={`${target.name} 多节点延迟`} eyebrow={`${rangeLabel} · ${target.reportingNodeCount} 个节点`} compactHeader peakCut={peakCut} />
        )}
      </section>
    </div>
  )
}

function ServiceInfoFact({ label, value, wide = false }: { label: string; value: string; wide?: boolean }) {
  return (
    <article className={`detail-fact${wide ? ' is-wide' : ''}`} title={`${label}: ${value}`}>
      <p>{label}</p>
      <strong>{value}</strong>
    </article>
  )
}

const detailRangeOptions = [
  { value: '1h', label: '实时' },
  { value: '1d', label: '1 天' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
]

function serviceTone(service: ServiceTarget): 'online' | 'warning' | 'offline' {
  if (service.reportingNodeCount <= 0) return 'offline'
  if (service.assignedNodeCount > 0 && service.reportingNodeCount < service.assignedNodeCount) return 'warning'
  if (service.lossPercent !== null && service.lossPercent >= 20) return 'warning'
  return 'online'
}

function formatServiceEndpoint(service: ServiceTarget): string {
  if (service.port !== undefined) return `${service.address}:${service.port}`
  return service.address
}

function formatServiceLatency(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  return `${Number.isInteger(value) ? value.toFixed(0) : value.toFixed(2)}ms`
}

function formatServiceLoss(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  return `${value.toFixed(2)}%`
}

function DashboardHeader({ settings = defaultSettings, onHome, onAdmin, adminLabel = '后台', trailingAction, onThemeToggle, onBackgroundToggle, backgroundEnabled = true }: DashboardHeaderProps) {
  const currentTheme = resolvedTheme(settings.theme)
  return (
    <header className="kulin-nav">
      <button className="brand" type="button" onClick={onHome}>
        <span className="brand-logo"><img src={settings.logoUrl || defaultSettings.logoUrl} alt={`${settings.siteTitle || 'Zeno'} logo`} /></span>
        <span>{settings.siteTitle || 'Zeno'}</span>
      </button>
      <nav className="nav-actions" aria-label="dashboard actions">
        <button className="login-link" type="button" onClick={onAdmin}>{adminLabel}</button>
        <button className="nav-icon-button is-solid" type="button" aria-label="language"><MapIcon /></button>
        <button className={`nav-icon-button${currentTheme === 'light' ? ' is-solid' : ''}`} type="button" aria-label={currentTheme === 'dark' ? '切换浅色模式' : '切换深色模式'} onClick={onThemeToggle}><SunIcon /><span className="sr-only">切换深浅色</span></button>
        <button className={`nav-icon-button${backgroundEnabled ? '' : ' is-muted'}`} type="button" aria-label={backgroundEnabled ? '关闭背景图' : '开启背景图'} onClick={onBackgroundToggle}><ImageMinusIcon /><span className="sr-only">开关背景图</span></button>
        {trailingAction}
      </nav>
    </header>
  )
}

interface AdminDashboardProps {
  onHome: () => void
  settings?: AdminSettings
  chromeSettings?: AdminSettings
  hasAdminToken?: boolean
  authState?: AdminAuthState
  adminState?: AdminLoadState
  initialSection?: AdminSection
  onAdminLogin?: (username: string, password: string) => void
  onAdminTokenClear?: () => void
  onAdminAccountUpdate?: (username: string, currentPassword: string, newPassword: string) => Promise<void>
  onAdminNodeCreate?: (input: AdminNodeCreateInput) => Promise<AdminNode | void>
  onAdminNodeUpdate?: (nodeId: string, input: AdminNodeUpdateInput) => void
  onAdminNodeDelete?: (nodeId: string) => void
  onAdminInstallCommand?: (nodeId: string) => Promise<string>
  onAdminProbeTargetCreate?: (input: AdminProbeTargetInput) => void
  onAdminProbeTargetUpdate?: (targetId: string, input: AdminProbeTargetUpdateInput) => void
  onAdminProbeTargetDelete?: (targetId: string) => void
  onAdminNotificationChannelCreate?: (input: AdminNotificationChannelCreateInput) => void
  onAdminNotificationChannelUpdate?: (channelId: string, input: AdminNotificationChannelUpdateInput) => void
  onAdminNotificationChannelDelete?: (channelId: string) => void
  onAdminNotificationChannelTest?: (channelId: string) => void
  onAdminAlertRuleUpdate?: (ruleId: string, input: AdminAlertRuleUpdateInput) => void
  onAdminSettingsUpdate?: (input: AdminSettingsUpdateInput) => void
  onThemeToggle?: () => void
  onBackgroundToggle?: () => void
  backgroundEnabled?: boolean
}

export function AdminDashboard({
  onHome,
  settings = defaultSettings,
  chromeSettings = settings,
  hasAdminToken = false,
  authState = { kind: 'idle' },
  adminState = { kind: 'idle' },
  initialSection = 'nodes',
  onAdminLogin = () => {},
  onAdminTokenClear = () => {},
  onAdminAccountUpdate = () => Promise.reject(new Error('account update unavailable')),
  onAdminNodeCreate = () => Promise.resolve(),
  onAdminNodeUpdate = () => {},
  onAdminNodeDelete = () => {},
  onAdminInstallCommand = () => Promise.reject(new Error('install command unavailable')),
  onAdminProbeTargetCreate = () => {},
  onAdminProbeTargetUpdate = () => {},
  onAdminProbeTargetDelete = () => {},
  onAdminNotificationChannelCreate = () => {},
  onAdminNotificationChannelUpdate = () => {},
  onAdminNotificationChannelDelete = () => {},
  onAdminNotificationChannelTest = () => {},
  onAdminAlertRuleUpdate = () => {},
  onAdminSettingsUpdate = () => {},
  onThemeToggle,
  onBackgroundToggle,
  backgroundEnabled = true,
}: AdminDashboardProps) {
  const [activeSection, setActiveSection] = useState<AdminSection>(initialSection)
  const handleTokenSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const formData = new FormData(form)
    const username = String(formData.get('admin-username') ?? '').trim()
    const password = String(formData.get('admin-password') ?? '').trim()
    if (username === '' || password === '') return
    onAdminLogin(username, password)
  }

  return (
    <div className="kulin-container admin-container">
      <section className="home-top-card admin-panel" aria-label="admin dashboard">
        <DashboardHeader
          settings={chromeSettings}
          onHome={onHome}
          onAdmin={onHome}
          adminLabel="前台"
          trailingAction={hasAdminToken ? <button className="nav-logout-button" type="button" onClick={onAdminTokenClear}>退出</button> : undefined}
          onThemeToggle={onThemeToggle}
          onBackgroundToggle={onBackgroundToggle}
          backgroundEnabled={backgroundEnabled}
        />

        {!hasAdminToken && (
          <form className="admin-login-card" aria-label="admin login form" onSubmit={handleTokenSubmit}>
              <div className="admin-login-title">
                <strong>后台登录</strong>
              </div>
              <label>
                <span>账号</span>
                <input name="admin-username" autoComplete="username" placeholder="admin" aria-label="后台账号" />
              </label>
              <label>
                <span>密码</span>
                <input name="admin-password" type="password" autoComplete="current-password" placeholder="admin" aria-label="后台密码" />
              </label>
              <button type="submit" disabled={authState.kind === 'loading'}>{authState.kind === 'loading' ? '登录中…' : '登录后台'}</button>
              {authState.kind === 'error' && <p className="admin-login-error">{authState.message}</p>}
          </form>
        )}

        {hasAdminToken && (
          <>
            <div className="admin-toolbar">
              <AdminSectionNav
                activeSection={activeSection}
                onSectionChange={setActiveSection}
              />
            </div>

            {adminState.kind === 'loading' && <div className="admin-state-card">正在读取 Admin API…</div>}
            {adminState.kind === 'error' && <div className="admin-state-card is-error">Admin API 读取失败：{adminState.message}</div>}

            {adminState.kind === 'ready' && activeSection === 'nodes' && (
              <AdminNodeSection
                nodes={adminState.nodes}
                targets={adminState.targets}
                onCreate={onAdminNodeCreate}
                onUpdate={onAdminNodeUpdate}
                onDelete={onAdminNodeDelete}
                onTargetUpdate={onAdminProbeTargetUpdate}
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

            {adminState.kind === 'ready' && activeSection === 'account' && (
              <AdminAccountSection account={adminState.account} onUpdate={onAdminAccountUpdate} />
            )}

            {adminState.kind === 'ready' && activeSection === 'settings' && (
              <AdminSettingsSection settings={settings} onUpdate={onAdminSettingsUpdate} />
            )}

            {adminState.kind === 'ready' && activeSection === 'notifications' && (
              <AdminNotificationsSection
                channels={adminState.notificationChannels}
                onChannelCreate={onAdminNotificationChannelCreate}
                onChannelUpdate={onAdminNotificationChannelUpdate}
                onChannelDelete={onAdminNotificationChannelDelete}
                onChannelTest={onAdminNotificationChannelTest}
                rules={adminState.alertRules}
                nodes={adminState.nodes}
                onRuleUpdate={onAdminAlertRuleUpdate}
              />
            )}
          </>
        )}
      </section>
    </div>
  )
}

function AdminSectionNav({ activeSection, onSectionChange }: { activeSection: AdminSection; onSectionChange: (section: AdminSection) => void }) {
  const sections: Array<{ id: AdminSection; label: string }> = [
    { id: 'nodes', label: '服务器' },
    { id: 'targets', label: '延迟监控' },
    { id: 'notifications', label: '通知' },
    { id: 'account', label: '账户' },
    { id: 'settings', label: '设置' },
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
        </button>
      ))}
    </nav>
  )
}

function AdminAccountSection({ account, onUpdate }: { account: AdminAccountData; onUpdate: (username: string, currentPassword: string, newPassword: string) => Promise<void> }) {
  const [message, setMessage] = useState<{ kind: 'error' | 'success'; text: string } | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const username = String(formData.get('account-username') ?? '').trim()
    const currentPassword = String(formData.get('current-password') ?? '').trim()
    const newPassword = String(formData.get('new-password') ?? '').trim()
    const confirmPassword = String(formData.get('confirm-password') ?? '').trim()
    if (!validAdminAccountUsername(username)) {
      setMessage({ kind: 'error', text: '账号只能使用 3-64 位字母、数字、点、短横线或下划线。' })
      return
    }
    if (currentPassword === '') {
      setMessage({ kind: 'error', text: '请输入当前密码确认修改。' })
      return
    }
    if (newPassword !== '' && newPassword.length < 8) {
      setMessage({ kind: 'error', text: '新密码至少 8 位；不改密码可留空。' })
      return
    }
    if (newPassword !== confirmPassword) {
      setMessage({ kind: 'error', text: '两次输入的新密码不一致。' })
      return
    }
    setSubmitting(true)
    setMessage(null)
    onUpdate(username, currentPassword, newPassword)
      .then(() => setMessage({ kind: 'success', text: '账户已更新。' }))
      .catch((error: unknown) => setMessage({ kind: 'error', text: error instanceof Error ? error.message : '账户更新失败。' }))
      .finally(() => setSubmitting(false))
  }

  return (
    <section className="admin-account-section admin-workspace-panel" aria-label="账户设置">
      <header className="admin-section-heading">
        <div>
          <p className="eyebrow">账号密码</p>
          <h3>账户</h3>
        </div>
      </header>
      <form className="admin-account-form admin-node-edit-form is-sectioned" aria-label="修改账号和密码" onSubmit={handleSubmit}>
        <AdminFormSection title="登录信息">
          <div className="admin-form-grid">
            <label>
              <span>账号</span>
              <input name="account-username" autoComplete="username" defaultValue={account.username} />
            </label>
            <label>
              <span>当前密码</span>
              <input name="current-password" type="password" autoComplete="current-password" />
            </label>
          </div>
        </AdminFormSection>
        <AdminFormSection title="修改密码" description="不改密码时，新密码和确认新密码可以留空。">
          <div className="admin-form-grid">
            <label>
              <span>新密码</span>
              <input name="new-password" type="password" autoComplete="new-password" placeholder="留空则不修改" />
            </label>
            <label>
              <span>确认新密码</span>
              <input name="confirm-password" type="password" autoComplete="new-password" placeholder="留空则不修改" />
            </label>
          </div>
        </AdminFormSection>
        <div className="admin-modal-actions">
          <button type="submit" disabled={submitting}>{submitting ? '保存中…' : '保存账户'}</button>
        </div>
        {message && <p className={`admin-install-error${message.kind === 'success' ? ' is-success' : ''}`}>{message.text}</p>}
      </form>
    </section>
  )
}

function validAdminAccountUsername(username: string): boolean {
  return /^[A-Za-z0-9._-]{3,64}$/.test(username.trim())
}

function AdminSettingsSection({ settings, onUpdate }: { settings: AdminSettings; onUpdate: (input: AdminSettingsUpdateInput) => void }) {
  const [settingsError, setSettingsError] = useState<string | null>(null)
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const theme = String(formData.get('theme') ?? 'system') as AdminSettings['theme']
    const input: AdminSettingsUpdateInput = {
      siteTitle: String(formData.get('site-title') ?? '').trim(),
      siteSubtitle: String(formData.get('site-subtitle') ?? '').trim(),
      logoUrl: String(formData.get('logo-url') ?? '').trim(),
      theme,
      agentControllerUrl: String(formData.get('agent-controller-url') ?? '').trim(),
      backgroundUrl: String(formData.get('desktop-background-url') ?? '').trim(),
      desktopBackgroundUrl: String(formData.get('desktop-background-url') ?? '').trim(),
      mobileBackgroundUrl: String(formData.get('mobile-background-url') ?? '').trim(),
    }
    const validationError = validateAdminSettingsInput(input)
    if (validationError) {
      setSettingsError(validationError)
      return
    }
    setSettingsError(null)
    onUpdate(input)
  }

  return (
    <section className="admin-settings-section admin-workspace-panel" aria-label="admin settings">
      <header className="admin-section-heading">
        <div>
          <p className="eyebrow">外观</p>
          <h3>站点设置</h3>
        </div>
      </header>
      <form className="admin-settings-form admin-node-edit-form is-sectioned" aria-label="外观配置" onSubmit={handleSubmit}>
        <AdminFormSection title="站点信息">
          <div className="admin-form-grid">
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
          </div>
        </AdminFormSection>
        <AdminFormSection title="主题与背景">
          <div className="admin-form-grid">
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
          </div>
        </AdminFormSection>
        <AdminFormSection title="Agent 接入">
          <div className="admin-form-grid">
            <label>
              <span>Agent 接入 URL</span>
              <input name="agent-controller-url" autoComplete="off" defaultValue={settings.agentControllerUrl} placeholder="留空则使用当前后台访问地址" />
            </label>
          </div>
        </AdminFormSection>
        {settingsError && <p className="admin-install-error">{settingsError}</p>}
        <button type="submit">保存设置</button>
      </form>
    </section>
  )
}

export function validateAdminSettingsInput(input: AdminSettingsUpdateInput): string | null {
  if (!validSettingsImageURL(input.logoUrl ?? '')) return '头像 / Logo URL 只能是 https:// 链接或 /assets/... 站内路径。'
  if (!validSettingsImageURL(input.desktopBackgroundUrl ?? input.backgroundUrl ?? '')) return '电脑端背景图 URL 只能是 https:// 链接或 /assets/... 站内路径。'
  if (!validSettingsImageURL(input.mobileBackgroundUrl ?? '')) return '手机端背景图 URL 只能是 https:// 链接或 /assets/... 站内路径。'
  if (!validAgentControllerURL(input.agentControllerUrl ?? '')) return 'Agent 接入 URL 只能是 http:// 或 https://，且不能包含用户名密码、query 或 fragment。'
  return null
}

function validSettingsImageURL(value: string): boolean {
  const trimmed = value.trim()
  if (trimmed === '') return true
  if (trimmed.startsWith('/') && !trimmed.startsWith('//')) return true
  try {
    const parsed = new URL(trimmed)
    return parsed.protocol === 'https:' && parsed.hostname !== '' && parsed.username === '' && parsed.password === ''
  } catch {
    return false
  }
}

function validAgentControllerURL(value: string): boolean {
  const trimmed = value.trim().replace(/\/+$/, '')
  if (trimmed === '') return true
  try {
    const parsed = new URL(trimmed)
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:') && parsed.hostname !== '' && parsed.username === '' && parsed.password === '' && parsed.search === '' && parsed.hash === ''
  } catch {
    return false
  }
}

function AdminNodeSection({ nodes, targets, onCreate, onUpdate, onDelete, onTargetUpdate, onInstallCommand }: { nodes: AdminNode[]; targets: AdminProbeTarget[]; onCreate: (input: AdminNodeCreateInput) => Promise<AdminNode | void>; onUpdate: (nodeId: string, input: AdminNodeUpdateInput) => void; onDelete: (nodeId: string) => void; onTargetUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => void; onInstallCommand: (nodeId: string) => Promise<string> }) {
  const [creatingNode, setCreatingNode] = useState(false)
  const [editingNodeId, setEditingNodeId] = useState<string | null>(null)
  const [sortingNodes, setSortingNodes] = useState(false)
  const editingNode = editingNodeId ? nodes.find((node) => node.id === editingNodeId) : undefined
  const orderedNodes = sortAdminNodes(nodes)
  const applyOrderPatches = (orderedNodes: AdminNode[]) => {
    const patches = buildAdminNodeOrderPatches(orderedNodes)
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
          <button type="button" onClick={() => setSortingNodes(true)}>服务器排序</button>
          <button className="admin-primary-action" type="button" onClick={() => setCreatingNode(true)}>添加服务器</button>
        </div>
      </header>

      {nodes.length === 0 && <div className="admin-state-card">还没有节点。</div>}
      {nodes.length > 0 && <AdminNodeList nodes={orderedNodes} onEdit={setEditingNodeId} onDelete={onDelete} />}

      {creatingNode && (
        <AdminNodeCreateModal
          onClose={() => setCreatingNode(false)}
          onCreate={onCreate}
          onInstallCommand={onInstallCommand}
        />
      )}

      {editingNode && (
        <AdminNodeEditModal
          key={editingNode.id}
          node={editingNode}
          targets={targets}
          onClose={() => setEditingNodeId(null)}
          onUpdate={(nodeId, input) => {
            onUpdate(nodeId, input)
            setEditingNodeId(null)
          }}
          onTargetUpdate={onTargetUpdate}
          onInstallCommand={onInstallCommand}
        />
      )}

      {sortingNodes && (
        <AdminNodeSortModal
          nodes={orderedNodes}
          onClose={() => setSortingNodes(false)}
          onSave={(nextNodes) => {
            applyOrderPatches(nextNodes)
            setSortingNodes(false)
          }}
        />
      )}
    </section>
  )
}

type AdminNodeOrderPatch = { nodeId: string; displayOrder: number }

function AdminNodeList({ nodes, onEdit, onDelete }: { nodes: AdminNode[]; onEdit: (nodeId: string) => void; onDelete: (nodeId: string) => void }) {
  const confirmDelete = (node: AdminNode) => {
    const ok = typeof window === 'undefined' ? true : window.confirm(`确认删除服务器「${node.displayName}」？这会删除该服务器的历史上报和探测记录。`)
    if (ok) onDelete(node.id)
  }

  return (
    <div className="admin-list" role="list" aria-label="服务器列表">
      <div className="admin-list-head" aria-hidden="true">
        <span>服务器</span>
        <span>公网 IP</span>
        <span>Agent 版本</span>
        <span>操作</span>
      </div>
      {nodes.map((node) => (
        <article className="admin-list-row" role="listitem" key={node.id}>
          <div className="admin-list-main">
            <strong>{node.displayName}</strong>
          </div>
          <span data-label="公网 IP" className={`admin-ip-stack${node.publicIPv6 ? '' : ' is-single'}`}>
            {node.publicIPv4 && <span>{node.publicIPv4}</span>}
            {node.publicIPv6 && <span>{node.publicIPv6}</span>}
            {!node.publicIPv4 && !node.publicIPv6 && <span>—</span>}
          </span>
          <span data-label="Agent 版本">{node.agentVersion || '—'}</span>
          <div className="admin-row-actions admin-icon-actions">
            <button className="admin-row-action is-icon" type="button" aria-label={`编辑服务器 ${node.displayName}`} title="编辑服务器" onClick={() => onEdit(node.id)}><EditActionIcon /><span className="sr-only">编辑服务器</span></button>
            <button className="admin-row-action is-icon is-danger" type="button" aria-label={`删除服务器 ${node.displayName}`} title="删除服务器" onClick={() => confirmDelete(node)}><TrashActionIcon /><span className="sr-only">删除服务器</span></button>
          </div>
        </article>
      ))}
    </div>
  )
}

function sortAdminNodes(nodes: AdminNode[]): AdminNode[] {
  return [...nodes].sort((left, right) => left.displayOrder - right.displayOrder || left.id.localeCompare(right.id, 'zh-CN'))
}

function buildAdminNodeOrderPatches(nodes: AdminNode[]): AdminNodeOrderPatch[] {
  const orderedNodes = [...nodes]
  return orderedNodes
    .map((node, index) => ({ nodeId: node.id, displayOrder: (index + 1) * 10 }))
    .filter((patch) => orderedNodes.find((node) => node.id === patch.nodeId)?.displayOrder !== patch.displayOrder)
}

function moveAdminNodeInOrder(nodeIds: string[], sourceId: string, targetId: string): string[] {
  const sourceIndex = nodeIds.indexOf(sourceId)
  const targetIndex = nodeIds.indexOf(targetId)
  if (sourceIndex < 0 || targetIndex < 0 || sourceIndex === targetIndex) return nodeIds
  const nextIds = [...nodeIds]
  const [source] = nextIds.splice(sourceIndex, 1)
  nextIds.splice(targetIndex, 0, source)
  return nextIds
}

function AdminNodeSortModal({ nodes, onSave, onClose }: { nodes: AdminNode[]; onSave: (nodes: AdminNode[]) => void; onClose: () => void }) {
  const [orderedIds, setOrderedIds] = useState(() => nodes.map((node) => node.id))
  const [draggedNodeId, setDraggedNodeId] = useState<string | null>(null)
  const nodeById = new Map(nodes.map((node) => [node.id, node]))
  const orderedNodes = orderedIds.map((nodeId) => nodeById.get(nodeId)).filter((node): node is AdminNode => Boolean(node))
  const moveNode = (sourceId: string, targetId: string) => {
    setOrderedIds((currentIds) => moveAdminNodeInOrder(currentIds, sourceId, targetId))
  }
  const handleDragStart = (event: DragEvent<HTMLElement>, nodeId: string) => {
    setDraggedNodeId(nodeId)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', nodeId)
  }
  const handleDragOver = (event: DragEvent<HTMLElement>, targetId: string) => {
    event.preventDefault()
    const sourceId = draggedNodeId || event.dataTransfer.getData('text/plain')
    if (!sourceId || sourceId === targetId) return
    moveNode(sourceId, targetId)
    setDraggedNodeId(sourceId)
  }

  return (
    <AdminModalLayer><div className="admin-modal-backdrop" role="presentation">
      <div className="admin-modal" role="dialog" aria-modal="true" aria-label="服务器排序">
        <header className="admin-modal-header">
          <div>
            <p className="eyebrow">Servers</p>
            <h3>服务器排序</h3>
          </div>
          <button className="admin-modal-close" type="button" aria-label="关闭" onClick={onClose}>×</button>
        </header>
        <p className="admin-help-note">按住服务器拖动调整顺序；保存后这里的顺序就是前台显示顺序。</p>
        <div className="admin-server-sort-list" role="list" aria-label="拖动排序服务器">
          {orderedNodes.map((node, index) => (
            <article
              className={`admin-server-sort-item${draggedNodeId === node.id ? ' is-dragging' : ''}`}
              role="listitem"
              draggable
              key={node.id}
              onDragStart={(event) => handleDragStart(event, node.id)}
              onDragOver={(event) => handleDragOver(event, node.id)}
              onDrop={(event) => event.preventDefault()}
              onDragEnd={() => setDraggedNodeId(null)}
            >
              <span className="admin-drag-handle" aria-hidden="true">⋮⋮</span>
              <span className="admin-server-sort-index">{index + 1}</span>
              <strong>{node.displayName}</strong>
            </article>
          ))}
        </div>
        <div className="admin-modal-actions">
          <button type="button" onClick={onClose}>取消</button>
          <button className="admin-primary-action" type="button" onClick={() => onSave(orderedNodes)}>保存排序</button>
        </div>
      </div>
    </div></AdminModalLayer>
  )
}

function AdminNodeCreateModal({ onCreate, onInstallCommand, onClose }: { onCreate: (input: AdminNodeCreateInput) => Promise<AdminNode | void>; onInstallCommand: (nodeId: string) => Promise<string>; onClose: () => void }) {
  const [createdNode, setCreatedNode] = useState<AdminNode | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [installCommandState, setInstallCommandState] = useState<{ kind: 'idle' } | { kind: 'loading' } | { kind: 'ready'; command: string } | { kind: 'error'; message: string }>({ kind: 'idle' })
  const [installCopyState, setInstallCopyState] = useState<{ kind: 'idle' } | { kind: 'ready'; message: string } | { kind: 'error'; message: string }>({ kind: 'idle' })

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const displayName = String(formData.get('new-display-name') ?? '').trim()
    if (displayName === '') {
      setFormError('请先填写服务器名称。')
      return
    }
    setSubmitting(true)
    setFormError(null)
    onCreate({
      displayName,
      expiryDate: String(formData.get('new-expiry-date') ?? '').trim(),
      billingCycle: String(formData.get('new-billing-cycle') ?? '').trim(),
      billingMode: String(formData.get('new-billing-mode') ?? 'both'),
      monthlyResetDay: parseMonthlyResetDay(String(formData.get('new-monthly-reset-day') ?? '')) ?? 1,
      monthlyQuotaBytes: parseQuotaGigabytes(String(formData.get('new-monthly-quota-gb') ?? '')),
    })
      .then((node) => {
        if (node) setCreatedNode(node)
      })
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '添加服务器失败'))
      .finally(() => setSubmitting(false))
  }

  const handleInstallCommand = () => {
    if (!createdNode) return
    setInstallCommandState({ kind: 'loading' })
    setInstallCopyState({ kind: 'idle' })
    onInstallCommand(createdNode.id)
      .then((command) => setInstallCommandState({ kind: 'ready', command }))
      .catch((error: unknown) => setInstallCommandState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const handleCopyInstallCommand = () => {
    if (installCommandState.kind !== 'ready') return
    copyTextToClipboard(installCommandState.command)
      .then(() => setInstallCopyState({ kind: 'ready', message: '安装命令已复制。' }))
      .catch((error: unknown) => setInstallCopyState({ kind: 'error', message: error instanceof Error ? error.message : '复制失败，请手动选中复制。' }))
  }

  return (
    <AdminModal title="添加服务器" eyebrow="Servers" onClose={onClose}>
      <form className="admin-node-create-form admin-node-edit-form is-sectioned" aria-label="添加服务器" onSubmit={handleSubmit}>
        <AdminFormSection title="服务器名称">
          <div className="admin-form-grid">
            <label>
              <span>服务器名称</span>
              <input name="new-display-name" autoComplete="off" placeholder="New Server" disabled={Boolean(createdNode)} />
            </label>
          </div>
        </AdminFormSection>
        <AdminFormSection title="账单与流量" description="账单信息可以留空，后续再补。">
          <div className="admin-form-grid">
            <label>
              <span>到期日</span>
              <input name="new-expiry-date" type="date" autoComplete="off" disabled={Boolean(createdNode)} />
            </label>
            <label>
              <span>账单周期</span>
              <input name="new-billing-cycle" autoComplete="off" placeholder="月付 / 年付" disabled={Boolean(createdNode)} />
            </label>
            <label>
              <span>流量计费口径</span>
              <select name="new-billing-mode" defaultValue="both" disabled={Boolean(createdNode)}>
                <option value="both">入站 + 出站</option>
                <option value="in">只算入站</option>
                <option value="out">只算出站</option>
                <option value="max">入/出取较大值</option>
              </select>
            </label>
            <label>
              <span>月流量重置日</span>
              <input name="new-monthly-reset-day" type="number" min="1" max="31" step="1" defaultValue="1" disabled={Boolean(createdNode)} />
            </label>
            <label>
              <span>月配额 GB</span>
              <input name="new-monthly-quota-gb" type="number" min="0" step="0.01" disabled={Boolean(createdNode)} />
            </label>
          </div>
        </AdminFormSection>
        <AdminFormSection title="Agent 接入" description={createdNode ? '服务器已添加，可以直接生成 Agent 安装命令。' : '先添加服务器，随后在这里生成 Agent 安装命令。'}>
          {createdNode && <p className="admin-help-note">已添加：{createdNode.displayName}</p>}
          <div className="admin-inline-actions">
            <button type="button" onClick={handleInstallCommand} disabled={!createdNode || installCommandState.kind === 'loading'}>{installCommandState.kind === 'loading' ? '生成中…' : '生成安装命令'}</button>
            <button type="button" onClick={handleCopyInstallCommand} disabled={installCommandState.kind !== 'ready'}>复制安装命令</button>
          </div>
          {installCommandState.kind === 'ready' && (
            <textarea className="admin-install-command" aria-label="新服务器 Agent 安装命令" readOnly value={installCommandState.command} />
          )}
          {installCopyState.kind !== 'idle' && <div className={`admin-install-error${installCopyState.kind === 'ready' ? ' is-success' : ''}`}>{installCopyState.message}</div>}
          {installCommandState.kind === 'error' && <div className="admin-install-error">安装命令生成失败：{installCommandState.message}</div>}
        </AdminFormSection>
        {formError && <div className="admin-install-error">{formError}</div>}
        <div className="admin-modal-actions">
          <button type="submit" disabled={submitting || Boolean(createdNode)}>{submitting ? '添加中…' : createdNode ? '服务器已添加' : '添加服务器'}</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminNodeEditModal({ node, targets, onUpdate, onTargetUpdate, onInstallCommand, onClose }: { node: AdminNode; targets: AdminProbeTarget[]; onUpdate: (nodeId: string, input: AdminNodeUpdateInput) => void; onTargetUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => void; onInstallCommand: (nodeId: string) => Promise<string>; onClose: () => void }) {
  const [installCommandState, setInstallCommandState] = useState<{ kind: 'idle' } | { kind: 'loading' } | { kind: 'ready'; command: string } | { kind: 'error'; message: string }>({ kind: 'idle' })
  const [installCopyState, setInstallCopyState] = useState<{ kind: 'idle' } | { kind: 'ready'; message: string } | { kind: 'error'; message: string }>({ kind: 'idle' })
  const sortedTargets = sortAdminProbeTargets(targets)
  const initialSelectedTargetIds = sortedTargets.filter((target) => target.assignments.some((assignment) => assignment.nodeId === node.id && assignment.enabled)).map((target) => target.id)
  const [selectedTargetIds, setSelectedTargetIds] = useState<string[]>(initialSelectedTargetIds)
  const [homeTargetId, setHomeTargetId] = useState<string>(node.homeProbeTargetId && initialSelectedTargetIds.includes(node.homeProbeTargetId) ? node.homeProbeTargetId : '')

  const updateSelectedTargetIds = (nextTargetIds: string[]) => {
    setSelectedTargetIds(nextTargetIds)
    if (homeTargetId !== '' && !nextTargetIds.includes(homeTargetId)) {
      setHomeTargetId('')
    }
  }

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const displayName = String(formData.get('display-name') ?? '').trim()
    const selectedTargets = new Set(selectedTargetIds)
    onUpdate(node.id, {
      displayName: displayName || node.displayName,
      homeProbeTargetId: selectedTargets.has(homeTargetId) ? homeTargetId : '',
      expiryDate: String(formData.get('expiry-date') ?? '').trim(),
      billingCycle: String(formData.get('billing-cycle') ?? '').trim(),
      billingMode: String(formData.get('billing-mode') ?? node.billingMode),
      monthlyResetDay: parseMonthlyResetDay(String(formData.get('monthly-reset-day') ?? '')) ?? node.monthlyResetDay,
      monthlyQuotaBytes: parseQuotaGigabytes(String(formData.get('monthly-quota-gb') ?? '')),
    })
    sortedTargets.forEach((target) => {
      const currentEnabled = target.assignments.some((assignment) => assignment.nodeId === node.id && assignment.enabled)
      const nextEnabled = selectedTargets.has(target.id)
      if (currentEnabled !== nextEnabled) {
        onTargetUpdate(target.id, { assignments: [{ nodeId: node.id, enabled: nextEnabled }] })
      }
    })
  }

  const handleInstallCommand = () => {
    const shouldConfirmRotation = !node.disabled && node.status !== 'no_data'
    if (shouldConfirmRotation) {
      const ok = typeof window === 'undefined' ? true : window.confirm('生成安装命令会轮换该服务器的 Agent Token；当前 Agent 需要用新命令重新安装后才会继续上报。确认继续？')
      if (!ok) return
    }
    setInstallCommandState({ kind: 'loading' })
    setInstallCopyState({ kind: 'idle' })
    onInstallCommand(node.id)
      .then((command) => setInstallCommandState({ kind: 'ready', command }))
      .catch((error: unknown) => setInstallCommandState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const handleCopyInstallCommand = () => {
    if (installCommandState.kind !== 'ready') return
    copyTextToClipboard(installCommandState.command)
      .then(() => setInstallCopyState({ kind: 'ready', message: '安装命令已复制。' }))
      .catch((error: unknown) => setInstallCopyState({ kind: 'error', message: error instanceof Error ? error.message : '复制失败，请手动选中复制。' }))
  }

  return (
    <AdminModal title={`编辑服务器 · ${node.displayName}`} eyebrow={node.agentVersion ? `Agent ${node.agentVersion}` : 'Agent 版本未知'} onClose={onClose}>
      <form className="admin-node-edit-form is-sectioned" aria-label={`${node.displayName} 节点编辑`} onSubmit={handleSubmit}>
        <AdminFormSection title="服务器名称">
          <div className="admin-form-grid">
            <label>
              <span>服务器名称</span>
              <input name="display-name" defaultValue={node.displayName} autoComplete="off" />
            </label>
          </div>
        </AdminFormSection>
        <AdminFormSection title="关联延迟监控" description="选择这台服务器要执行的延迟监控目标。">
          {sortedTargets.length === 0 ? (
            <div className="admin-state-card is-compact">暂无延迟监控。</div>
          ) : (
            <AdminExpandedCheckList
              title="已选延迟监控"
              emptyText="暂无延迟监控"
              options={sortedTargets.map((target) => ({ value: target.id, label: target.name }))}
              value={selectedTargetIds}
              onChange={updateSelectedTargetIds}
              renderRight={(option, checked) => (
                <label className={`admin-home-monitor-radio${checked ? '' : ' is-disabled'}`}>
                  <input
                    type="radio"
                    name={`home-monitor-${node.id}`}
                    checked={checked && homeTargetId === option.value}
                    disabled={!checked}
                    onChange={() => setHomeTargetId(option.value)}
                  />
                  <span>首页展示</span>
                </label>
              )}
            />
          )}
        </AdminFormSection>
        <AdminFormSection title="账单与流量" description="账单信息可以留空，后续再补。">
          <div className="admin-form-grid">
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
              <span>月配额 GB</span>
              <input name="monthly-quota-gb" type="number" min="0" step="0.01" defaultValue={formatQuotaGigabytes(node.monthlyQuotaBytes)} />
            </label>
          </div>
        </AdminFormSection>
        <AdminFormSection title="Agent 接入" description="生成安装命令会轮换该服务器的 Agent Token；已在线服务器执行新命令前会停止上报。">
          <p className="admin-help-note">当前 Agent 版本：{node.agentVersion || '暂无上报'}</p>
          <div className="admin-inline-actions">
            <button type="button" onClick={handleInstallCommand} disabled={installCommandState.kind === 'loading'}>{installCommandState.kind === 'loading' ? '生成中…' : '轮换并生成安装命令'}</button>
            <button type="button" onClick={handleCopyInstallCommand} disabled={installCommandState.kind !== 'ready'}>复制安装命令</button>
          </div>
          {installCommandState.kind === 'ready' && (
            <textarea className="admin-install-command" aria-label={`${node.displayName} Agent 安装命令`} readOnly value={installCommandState.command} />
          )}
          {installCopyState.kind !== 'idle' && <div className={`admin-install-error${installCopyState.kind === 'ready' ? ' is-success' : ''}`}>{installCopyState.message}</div>}
          {installCommandState.kind === 'error' && <div className="admin-install-error">安装命令生成失败：{installCommandState.message}</div>}
        </AdminFormSection>
        <div className="admin-modal-actions">
          <button type="submit">保存服务器</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminTargetSection({ targets, nodes, onCreate, onUpdate, onDelete }: { targets: AdminProbeTarget[]; nodes: AdminNode[]; onCreate: (input: AdminProbeTargetInput) => void; onUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => void; onDelete: (targetId: string) => void }) {
  const [creatingTarget, setCreatingTarget] = useState(false)
  const [editingTargetId, setEditingTargetId] = useState<string | null>(null)
  const editingTarget = editingTargetId ? targets.find((target) => target.id === editingTargetId) : undefined
  const sortedTargets = sortAdminProbeTargets(targets)

  return (
    <section className="admin-target-section admin-workspace-panel" aria-label="admin probe target list">
      <header className="admin-section-heading">
        <div>
          <p className="eyebrow">Latency</p>
          <h3>延迟监控</h3>
        </div>
        <div className="admin-section-actions">
          <button className="admin-primary-action" type="button" onClick={() => setCreatingTarget(true)}>添加目标</button>
        </div>
      </header>

      {targets.length === 0 && <div className="admin-state-card">还没有探针目标。</div>}
      {targets.length > 0 && <AdminTargetList targets={sortedTargets} onEdit={setEditingTargetId} onDelete={onDelete} />}

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

function AdminTargetList({ targets, onEdit, onDelete }: { targets: AdminProbeTarget[]; onEdit: (targetId: string) => void; onDelete: (targetId: string) => void }) {
  const confirmDelete = (target: AdminProbeTarget) => {
    const ok = typeof window === 'undefined' ? true : window.confirm(`确认删除延迟监控「${target.name}」？这会删除该目标的历史探测记录。`)
    if (ok) onDelete(target.id)
  }

  return (
    <div className="admin-list admin-target-list" role="list" aria-label="延迟监控目标列表">
      <div className="admin-list-head" aria-hidden="true">
        <span>目标</span>
        <span>地址</span>
        <span>节点</span>
        <span>操作</span>
      </div>
      {targets.map((target) => (
        <article className="admin-list-row" role="listitem" key={target.id}>
          <div className="admin-list-main">
            <strong>{target.name}</strong>
          </div>
          <span data-label="地址">{formatTargetEndpoint(target)}</span>
          <span data-label="节点">{formatTargetAssignmentSummary(target)}</span>
          <div className="admin-row-actions admin-icon-actions">
            <button className="admin-row-action is-icon" type="button" aria-label={`编辑目标 ${target.name}`} title="编辑目标" onClick={() => onEdit(target.id)}><EditActionIcon /><span className="sr-only">编辑目标</span></button>
            <button className="admin-row-action is-icon is-danger" type="button" aria-label={`删除目标 ${target.name}`} title="删除目标" onClick={() => confirmDelete(target)}><TrashActionIcon /><span className="sr-only">删除目标</span></button>
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
    })
  }

  return (
    <AdminModal title="添加延迟监控目标" eyebrow="Latency" onClose={onClose}>
      <form className="admin-target-create-form admin-node-edit-form is-sectioned" aria-label="添加探针目标" onSubmit={handleSubmit}>
        <AdminFormSection title="目标信息">
          <div className="admin-form-grid">
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
          </div>
        </AdminFormSection>
        <AdminFormSection title="探测参数">
          <div className="admin-form-grid">
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
          </div>
        </AdminFormSection>
        <div className="admin-modal-actions">
          <button type="submit">添加目标</button>
        </div>
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
        <div><dt>类型</dt><dd>{formatTargetTypeLabel(target.type)}</dd></div>
        <div><dt>地址</dt><dd>{formatTargetEndpoint(target)}</dd></div>
        <div><dt>参数</dt><dd>{target.count} 次 / {target.timeoutMs}ms / {target.intervalSec}s</dd></div>
        <div><dt>节点</dt><dd>{formatTargetAssignmentSummary(target)}</dd></div>
      </dl>
      <form className="admin-target-edit-form admin-node-edit-form is-sectioned" aria-label={`${target.name} 探针目标编辑`} onSubmit={handleSubmit}>
        <AdminFormSection title="目标信息">
          <div className="admin-form-grid">
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
            <label className="admin-node-toggle">
              <input name="target-enabled" type="checkbox" defaultChecked={target.enabled} />
              <span>启用目标</span>
            </label>
          </div>
        </AdminFormSection>
        <AdminFormSection title="探测参数">
          <div className="admin-form-grid">
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
          </div>
        </AdminFormSection>
        {assignmentRows.length > 0 && (
          <AdminFormSection title="按节点启用">
            <div className="admin-target-assignment-list">
              {assignmentRows.map((assignment) => (
                <label className="admin-node-toggle admin-target-assignment-toggle" key={assignment.nodeId}>
                  <input name={`target-assignment-${assignment.nodeId}`} type="checkbox" defaultChecked={assignment.enabled} />
                  <span>{assignment.nodeDisplayName || assignment.nodeId}</span>
                </label>
              ))}
            </div>
          </AdminFormSection>
        )}
        <div className="admin-modal-actions">
          <button type="submit">保存目标</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminAlertRulesSection({ rules, nodes, onUpdate }: { rules: AdminAlertRule[]; nodes: AdminNode[]; onUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => void }) {
  const [editingRule, setEditingRule] = useState<AdminAlertRule | null>(null)
  const [addingRule, setAddingRule] = useState(false)
  const addedRules = rules.filter((rule) => rule.enabled)
  const availableRules = rules.filter((rule) => !rule.enabled)

  return (
    <section className="admin-notification-block admin-alert-rule-section" aria-label="通知类型规则">
      <div className="admin-block-heading">
        <h4>通知类型</h4>
        <button className="admin-row-action" type="button" onClick={() => setAddingRule(true)}>添加通知类型</button>
      </div>
      {addedRules.length === 0 && <div className="admin-state-card">还没有添加通知类型。</div>}
      {addedRules.length > 0 && <AdminAlertRuleList rules={addedRules} nodes={nodes} onEdit={setEditingRule} onUpdate={onUpdate} />}

      {addingRule && (
        <AdminAlertRuleAddModal
          rules={availableRules}
          nodes={nodes}
          onClose={() => setAddingRule(false)}
          onAdd={(ruleId) => {
            onUpdate(ruleId, { enabled: true })
            setAddingRule(false)
          }}
        />
      )}

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

function AdminAlertRuleList({ rules, nodes, onEdit, onUpdate }: { rules: AdminAlertRule[]; nodes: AdminNode[]; onEdit: (rule: AdminAlertRule) => void; onUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => void }) {
  const confirmDelete = (rule: AdminAlertRule) => {
    const ok = typeof window === 'undefined' ? true : window.confirm(`确认删除通知类型「${rule.name}」？`)
    if (ok) onUpdate(rule.id, { enabled: false })
  }

  return (
    <div className="admin-list admin-alert-rule-list" role="list" aria-label="通知类型列表">
      <div className="admin-list-head" aria-hidden="true">
        <span>通知类型</span>
        <span>范围</span>
        <span>状态</span>
        <span>操作</span>
      </div>
      {rules.map((rule) => (
        <article className="admin-list-row" role="listitem" key={rule.id}>
          <div className="admin-list-main">
            <strong>{rule.name}</strong>
          </div>
          <span data-label="范围">{formatAlertRuleScope(rule, nodes)}</span>
          <span data-label="状态" className={`admin-node-status status-${rule.enabled ? 'online' : 'disabled'}`}>{rule.enabled ? '启用中' : '已停用'}</span>
          <div className="admin-row-actions admin-icon-actions">
            <button className="admin-row-action is-icon" type="button" aria-label={`编辑通知类型 ${rule.name}`} title="编辑通知类型" onClick={() => onEdit(rule)}><EditActionIcon /><span className="sr-only">编辑通知类型</span></button>
            <button className="admin-row-action is-icon is-danger" type="button" aria-label={`删除通知类型 ${rule.name}`} title="删除通知类型" onClick={() => confirmDelete(rule)}><TrashActionIcon /><span className="sr-only">删除通知类型</span></button>
          </div>
        </article>
      ))}
    </div>
  )
}

function AdminAlertRuleAddModal({ rules, nodes, onAdd, onClose }: { rules: AdminAlertRule[]; nodes: AdminNode[]; onAdd: (ruleId: string) => void; onClose: () => void }) {
  return (
    <AdminModal title="添加通知类型" eyebrow="Notify" onClose={onClose}>
      <div className="admin-rule-picker" role="list" aria-label="可添加通知类型">
        {rules.length === 0 && <div className="admin-state-card">所有通知类型都已添加。</div>}
        {rules.map((rule) => (
          <article className="admin-rule-picker-row" role="listitem" key={rule.id}>
            <div className="admin-list-main">
              <strong>{rule.name}</strong>
              <small>{formatAlertRuleScope(rule, nodes)}</small>
            </div>
            <button className="admin-row-action" type="button" onClick={() => onAdd(rule.id)}>添加</button>
          </article>
        ))}
      </div>
    </AdminModal>
  )
}

function AdminAlertRuleEditModal({ rule, nodes, onUpdate, onClose }: { rule: AdminAlertRule; nodes: AdminNode[]; onUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => void; onClose: () => void }) {
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const scopeNodeIds = nodes.filter((node) => formData.get(`rule-scope-${node.id}`) === 'on').map((node) => node.id)
    onUpdate(rule.id, {
      enabled: formData.get('rule-enabled') === 'on',
      scopeNodeIds,
    })
  }

  return (
    <AdminModal title={`编辑通知类型 · ${rule.name}`} eyebrow={rule.id} onClose={onClose}>
      <dl className="admin-modal-summary">
        <div><dt>作用范围</dt><dd>{formatAlertRuleScope(rule, nodes)}</dd></div>
        <div><dt>通知类型</dt><dd>{rule.notificationLabel || rule.notificationEventType}</dd></div>
        <div><dt>当前状态</dt><dd>{rule.enabled ? '启用中' : '已停用'}</dd></div>
      </dl>
      <form className="admin-alert-rule-edit-form admin-node-edit-form is-sectioned" aria-label={`${rule.name} 通知类型编辑`} onSubmit={handleSubmit}>
        <AdminFormSection title="通知设置">
          <div className="admin-form-grid">
            <label className="admin-node-toggle">
              <input name="rule-enabled" type="checkbox" defaultChecked={rule.enabled} />
              <span>启用通知类型</span>
            </label>
          </div>
        </AdminFormSection>
        {nodes.length > 0 && (
          <AdminFormSection title="作用服务器" description="不选表示全部服务器。">
            <div className="admin-rule-scope-list admin-target-assignment-list">
              {nodes.map((node) => (
                <label className="admin-node-toggle admin-target-assignment-toggle" key={node.id}>
                  <input name={`rule-scope-${node.id}`} type="checkbox" defaultChecked={rule.scopeNodeIds.includes(node.id)} />
                  <span>{node.displayName || node.id}</span>
                </label>
              ))}
            </div>
          </AdminFormSection>
        )}
        <div className="admin-modal-actions">
          <button type="submit">保存通知类型</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminNotificationsSection({ channels, rules, nodes, onChannelCreate, onChannelUpdate, onChannelDelete, onChannelTest, onRuleUpdate }: { channels: AdminNotificationChannel[]; rules: AdminAlertRule[]; nodes: AdminNode[]; onChannelCreate: (input: AdminNotificationChannelCreateInput) => void; onChannelUpdate: (channelId: string, input: AdminNotificationChannelUpdateInput) => void; onChannelDelete: (channelId: string) => void; onChannelTest: (channelId: string) => void; onRuleUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => void }) {
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

      <AdminAlertRulesSection rules={rules} nodes={nodes} onUpdate={onRuleUpdate} />

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
          <span data-label="状态" className={`admin-node-status status-${channel.enabled ? 'online' : 'disabled'}`}>{channel.enabled ? '启用中' : '已停用'}</span>
          <span data-label="Chat ID" className="admin-notification-destination">{channel.destination}</span>
          <span data-label="Bot Token">{channel.credentialSet ? '凭据已设置' : '未设置凭据'}</span>
          <div className="admin-row-actions admin-icon-actions">
            <button className="admin-row-action" type="button" onClick={() => onTest(channel.id)}>测试发送</button>
            <button className="admin-row-action is-icon" type="button" aria-label={`编辑通知渠道 ${channel.name}`} title="编辑渠道" onClick={() => onEdit(channel)}><EditActionIcon /><span className="sr-only">编辑渠道</span></button>
            <button className="admin-row-action" type="button" onClick={() => onUpdate(channel.id, { enabled: !channel.enabled })}>
              {channel.enabled ? '停用渠道' : '启用渠道'}
            </button>
            <button className="admin-row-action is-icon is-danger" type="button" aria-label={`删除通知渠道 ${channel.name}`} title="删除渠道" onClick={() => confirmDelete(channel)}><TrashActionIcon /><span className="sr-only">删除渠道</span></button>
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
      <form className="admin-notification-edit-form admin-node-edit-form is-sectioned" aria-label="编辑通知渠道" onSubmit={handleSubmit}>
        <AdminFormSection title="渠道配置">
          <div className="admin-form-grid">
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
          </div>
        </AdminFormSection>
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
      <form className="admin-notification-create-form admin-node-edit-form is-sectioned" aria-label="添加通知渠道" onSubmit={handleSubmit}>
        <AdminFormSection title="渠道配置">
          <div className="admin-form-grid">
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
          </div>
        </AdminFormSection>
        <div className="admin-modal-actions">
          <button type="submit">保存通知渠道</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminModalLayer({ children }: { children: ReactNode }) {
  if (typeof document === 'undefined') return <>{children}</>
  return createPortal(children, document.body)
}

function AdminModal({ title, eyebrow, onClose, children }: { title: string; eyebrow: string; onClose: () => void; children: ReactNode }) {
  return (
    <AdminModalLayer>
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
    </AdminModalLayer>
  )
}

function AdminFormSection({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return (
    <fieldset className="admin-form-section">
      <legend>{title}</legend>
      {description && <p className="admin-form-section-note">{description}</p>}
      {children}
    </fieldset>
  )
}

type AdminExpandedCheckListOption = { value: string; label: string }

function AdminExpandedCheckList({ options, value, onChange, title = '已选', emptyText = '暂无可选项', renderRight }: { options: AdminExpandedCheckListOption[]; value: string[]; onChange: (value: string[]) => void; title?: string; emptyText?: string; renderRight?: (option: AdminExpandedCheckListOption, checked: boolean) => ReactNode }) {
  const [expanded, setExpanded] = useState(false)
  const optionValues = new Set(options.map((option) => option.value))
  const normalizedValue = Array.from(new Set((Array.isArray(value) ? value : []).filter((item) => optionValues.has(item))))
  const selected = new Set(normalizedValue)
  const toggleValue = (optionValue: string, checked: boolean) => {
    if (checked) {
      onChange(Array.from(new Set([...normalizedValue, optionValue])))
      return
    }
    onChange(normalizedValue.filter((item) => item !== optionValue))
  }

  return (
    <div className="admin-expanded-checklist">
      <button className="admin-expanded-checklist__trigger" type="button" aria-expanded={expanded} onClick={() => setExpanded((current) => !current)}>
        <span>{title} {normalizedValue.length}/{options.length}</span>
        <ChevronDownIcon expanded={expanded} />
      </button>
      {expanded && (
        <div className="admin-expanded-checklist__list" role="list">
          {options.length === 0 && <div className="admin-expanded-checklist__empty">{emptyText}</div>}
          {options.map((option) => {
            const checked = selected.has(option.value)
            return (
              <div className="admin-expanded-checklist__item" role="listitem" key={option.value}>
                <input type="checkbox" checked={checked} onChange={(event) => toggleValue(option.value, event.currentTarget.checked)} />
                <button type="button" title={option.label} onClick={() => toggleValue(option.value, !checked)}>{option.label}</button>
                {renderRight?.(option, checked)}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

function sortAdminProbeTargets(targets: AdminProbeTarget[]): AdminProbeTarget[] {
  return [...targets].sort((left, right) => left.displayOrder - right.displayOrder || left.id.localeCompare(right.id, 'zh-CN'))
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

function formatAlertRuleScope(rule: AdminAlertRule, nodes: AdminNode[]): string {
  if (rule.scopeNodeIds.length === 0) return '全部服务器'
  const labels = rule.scopeNodeIds.map((nodeId) => {
    const node = nodes.find((candidate) => candidate.id === nodeId)
    return node?.displayName || nodeId
  })
  return labels.join('、')
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

function copyTextToClipboard(text: string): Promise<void> {
  if (typeof navigator === 'undefined' || !navigator.clipboard?.writeText) {
    return Promise.reject(new Error('当前浏览器不支持自动复制，请手动选中复制。'))
  }
  return navigator.clipboard.writeText(text)
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

function formatAdminDate(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

export function HomeOverviewPanel({ totalCount, onlineCount, offlineCount: _offlineCount, totalUp, totalDown, upSpeed, downSpeed }: HomeOverviewPanelProps) {
  return (
    <section className="home-summary" aria-label="server overview">
      <div className="home-summary__status-line" aria-label="服务器在线摘要">
        <strong>{onlineCount} / {totalCount} 在线</strong>
      </div>

      <dl className="home-summary__metrics" aria-label="traffic totals and speeds">
        <div>
          <dt>上传</dt>
          <dd>{compactBytes(totalUp)}</dd>
        </div>
        <div>
          <dt>下载</dt>
          <dd>{compactBytes(totalDown)}</dd>
        </div>
        <div>
          <dt>实时</dt>
          <dd><CircleArrowIcon direction="up" />{compactRate(upSpeed)}</dd>
        </div>
        <div>
          <dt>实时</dt>
          <dd><CircleArrowIcon direction="down" />{compactRate(downSpeed)}</dd>
        </div>
      </dl>
    </section>
  )
}

function ChevronDownIcon({ expanded }: { expanded: boolean }) {
  return (
    <svg className={expanded ? 'is-expanded' : ''} viewBox="0 0 24 24" aria-hidden="true">
      <path d="m6 9 6 6 6-6" />
    </svg>
  )
}

function EditActionIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
    </svg>
  )
}

function TrashActionIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M3 6h18" />
      <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
      <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
      <path d="M10 11v6M14 11v6" />
    </svg>
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
