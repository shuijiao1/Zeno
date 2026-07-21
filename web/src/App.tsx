import { type CSSProperties, type DragEvent, type FormEvent, type ReactNode, useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import copy from 'copy-to-clipboard'
import { createAdminNode, createAdminNotificationChannel, createAdminProbeTarget, deleteAdminNode, deleteAdminNotificationChannel, deleteAdminProbeTarget, fetchAdminAlertRules, fetchAdminNodes, fetchAdminNotificationChannels, fetchAdminProbeTargets, fetchAdminSettings, fetchNodeLatency, fetchNodeState, fetchPublicSettings, fetchServiceLatency, fetchSummary, subscribeNodeLatency, subscribeNodeState, subscribeServiceLatency, subscribeSummary, requestAdminNodeInstallCommand, testAdminNotificationChannel, updateAdminAlertRule, updateAdminNode, updateAdminNotificationChannel, updateAdminProbeTarget, updateAdminSettings, type AdminAlertRuleUpdateInput, type AdminNodeCreateInput, type AdminNodeUpdateInput, type AdminNotificationChannelCreateInput, type AdminNotificationChannelUpdateInput, type AdminProbeTargetInput, type AdminProbeTargetUpdateInput, type AdminSettingsUpdateInput, type NodeLatencyData, type NodeStateData, type ServiceLatencyData, type SummaryData } from './api/client'
import { fetchAdminAccount, loginAdmin, logoutAdmin, updateAdminAccount, type AdminAccountData } from './api/adminSession'
import { LatencyDetail } from './components/LatencyDetail'
import { ServerCard } from './components/ServerCard'
import { ServiceDetail } from './components/ServiceDetail'
import { ServerFlag } from './components/ServerFlag'
import { startLiveRefresh } from './lib/liveRefresh'
import { startResilientLiveData } from './lib/resilientLive'
import { createMutationEpoch } from './lib/mutationEpoch'
import { adminCookieSessionMarker, captureAdminTokenIdentity, clearStoredAdminToken, clearStoredAdminTokenIfCurrent, loadStoredAdminToken, rememberAdminToken, type AdminTokenIdentity } from './lib/adminToken'
import { nodePath, parseDashboardRoute, type DashboardRoute } from './lib/route'
import { applyCustomCode } from './lib/customCode'
import { coerceHistoryRange, isHTTPUnauthorizedError, rangeRequiresAdmin } from './lib/historyRange'
import { DetailMemoryCache, loadCachedDetailData, nodeLatencyCachePrefix, nodeStateCachePrefix, rememberDetailData, serviceLatencyCachePrefix } from './lib/detailCache'
import { loadStoredSummary, rememberSummary } from './lib/summaryCache'
import type { AdminAlertRule, AdminNode, AdminNodeInstallCommand, AdminNotificationChannel, AdminProbeTarget, AdminSettings, AdminTheme, AppearancePreset, HomeCardNode, LatencyPoint, ProbeType, StatePoint } from './types'

export { applyCustomCode, extractSafeCustomCSS } from './lib/customCode'
export { availableHistoryRanges, coerceHistoryRange, rangeRequiresAdmin } from './lib/historyRange'
export { loadStoredSummary, rememberSummary } from './lib/summaryCache'
export { adminTokenMaxAgeMs } from './lib/adminToken'

type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; data: SummaryData; storedAt?: number; stale?: boolean }
  | { kind: 'error'; message: string }

const renewalDayOptions = [0, 1, 3, 7, 15, 30]

export function isAdminUnauthorizedError(error: unknown): boolean {
  return error instanceof Error && /^admin .+: 401$/.test(error.message)
}

function validateNodeLatencyData(value: unknown): NodeLatencyData | null {
  const data = value as Partial<NodeLatencyData> | null
  if (!data || typeof data.nodeId !== 'string' || typeof data.range !== 'string' || !Array.isArray(data.points)) return null
  return data as NodeLatencyData
}

function validateNodeStateData(value: unknown): NodeStateData | null {
  const data = value as Partial<NodeStateData> | null
  if (!data || typeof data.nodeId !== 'string' || typeof data.range !== 'string' || !Array.isArray(data.points)) return null
  return data as NodeStateData
}

function validateServiceLatencyData(value: unknown): ServiceLatencyData | null {
  const data = value as Partial<ServiceLatencyData> | null
  if (!data || !data.target || typeof data.range !== 'string' || !Array.isArray(data.points)) return null
  return data as ServiceLatencyData
}

function seedNodeLatencyFromSummary(summary: SummaryData | null, nodeId: string, range: string): NodeLatencyData | null {
  const node = summary?.nodes.find((item) => item.id === nodeId)
  if (!node) return null
  const summaries = node.latencySummaries && node.latencySummaries.length > 0
    ? node.latencySummaries
    : node.latencySummary ? [node.latencySummary] : []
  if (summaries.length === 0) return null
  return {
    nodeId,
    range,
    points: summaries.map((summary) => ({
      ts: summary.updatedAt || new Date().toISOString(),
      targetId: summary.targetId,
      targetName: summary.targetName,
      medianMs: summary.medianMs,
      avgMs: summary.avgMs ?? null,
      lossPercent: summary.lossPercent ?? 0,
    })),
  }
}

function seedNodeStateFromSummary(summary: SummaryData | null, nodeId: string, range: string): NodeStateData | null {
  const node = summary?.nodes.find((item) => item.id === nodeId)
  if (!node) return null
  const point: StatePoint = {
    ts: new Date().toISOString(),
    cpuPercent: node.cpuPercent,
    load1: node.load1 ?? null,
    load5: node.load5 ?? null,
    load15: node.load15 ?? null,
    memoryUsedBytes: node.memoryUsedBytes,
    memoryTotalBytes: node.memoryTotalBytes,
    swapUsedBytes: null,
    swapTotalBytes: null,
    diskUsedBytes: node.diskUsedBytes,
    diskTotalBytes: node.diskTotalBytes,
    netInTotalBytes: node.netInTotalBytes,
    netOutTotalBytes: node.netOutTotalBytes,
    netInSpeedBps: node.netInSpeedBps,
    netOutSpeedBps: node.netOutSpeedBps,
    processCount: null,
    tcpConnectionCount: null,
    udpConnectionCount: null,
    uptimeSeconds: node.uptimeSeconds ?? null,
  }
  return { nodeId, range, points: [point] }
}

type LatencyLoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; data: NodeLatencyData; freshness?: DataFreshness }
  | { kind: 'error'; message: string }

type StateHistoryLoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; data: NodeStateData; freshness?: DataFreshness }
  | { kind: 'error'; message: string }

type ServiceLatencyLoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; data: ServiceLatencyData; freshness?: DataFreshness }
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
type MaybePromise<T = void> = T | Promise<T>

function blurActiveElement() {
  if (typeof document === 'undefined') return
  const activeElement = document.activeElement
  if (activeElement instanceof HTMLElement || activeElement instanceof SVGElement) activeElement.blur()
}

type HomeRealtimeSnapshot = {
  nodes: HomeCardNode[]
  upSpeed: number
  downSpeed: number
}

type DataFreshness = {
  stale: boolean
  storedAt?: number
  source?: 'cache' | 'summary' | 'http' | 'ws'
}

const detailHttpFallbackDelayMs = 1800

const HOME_REALTIME_SNAPSHOT_INTERVAL_MS = 2000
const HOME_REALTIME_SNAPSHOT_FRAME_TOLERANCE_MS = 150
const HOME_REALTIME_STARTUP_SYNC_MS = 1000

function sum(values: Array<number | null | undefined>): number {
  return values.reduce<number>((total, value) => total + (value ?? 0), 0)
}

export function homeTrafficTotalsForNodes(nodes: HomeCardNode[]): { totalUp: number; totalDown: number } {
  return {
    totalUp: sum(nodes.map((node) => node.netOutLifetimeBytes ?? node.netOutTotalBytes)),
    totalDown: sum(nodes.map((node) => node.netInLifetimeBytes ?? node.netInTotalBytes)),
  }
}

function homeRealtimeSnapshotForNodes(nodes: HomeCardNode[]): HomeRealtimeSnapshot {
  return {
    nodes,
    upSpeed: sum(nodes.map((node) => node.netOutSpeedBps)),
    downSpeed: sum(nodes.map((node) => node.netInSpeedBps)),
  }
}

export function shouldRefreshHomeRealtimeSnapshot(lastUpdatedAt: number | null, now: number, mountedAt = now): boolean {
  return lastUpdatedAt === null || now - mountedAt <= HOME_REALTIME_STARTUP_SYNC_MS || now - lastUpdatedAt >= HOME_REALTIME_SNAPSHOT_INTERVAL_MS - HOME_REALTIME_SNAPSHOT_FRAME_TOLERANCE_MS
}

function monotonicNowMs(): number {
  return typeof performance !== 'undefined' ? performance.now() : Date.now()
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
  const amount = unit === 0 ? size.toFixed(0) : size.toFixed(2).replace(/\.00$/, '').replace(/(\.\d)0$/, '$1')
  return `${amount} ${units[unit]}`
}

function compactRateParts(value: number): { value: string; unit: string } {
  const [amount, unit = 'B'] = compactBytes(value).split(' ')
  return { value: amount, unit: `${unit}/s` }
}

function summaryLatencyPoints(node: HomeCardNode | undefined): LatencyPoint[] {
  return (node?.latencySummaries ?? [])
    .filter((summary) => summary.updatedAt)
    .map((summary) => ({
      ts: summary.updatedAt,
      targetId: summary.targetId,
      targetName: summary.targetName,
      medianMs: summary.medianMs,
      avgMs: summary.avgMs,
      lossPercent: summary.lossPercent ?? 0,
    }))
}

const defaultSettings: AdminSettings = {
  siteTitle: 'Zeno',
  siteSubtitle: '服务器运行概览',
  logoUrl: 'https://cdn.jsdelivr.net/gh/shuijiao1/Fly@main/ID-128.webp',
  theme: 'system',
  agentControllerUrl: '',
  backgroundUrl: '',
  desktopBackgroundUrl: '',
  mobileBackgroundUrl: '',
  appearancePreset: 'default',
  cardOpacity: 0.72,
  cardBlur: 0,
  cardRadius: 20,
  borderStrength: 0.26,
  shadowStrength: 0.22,
  backgroundOverlay: 0,
  themeColor: '#2563eb',
  customCode: '',
}

type AppearanceValues = Pick<AdminSettings, 'appearancePreset' | 'cardOpacity' | 'cardBlur' | 'cardRadius' | 'borderStrength' | 'shadowStrength' | 'backgroundOverlay' | 'themeColor'>

const appearancePresets: Record<AppearancePreset, AppearanceValues> = {
  default: {
    appearancePreset: 'default',
    cardOpacity: 0.72,
    cardBlur: 0,
    cardRadius: 20,
    borderStrength: 0.26,
    shadowStrength: 0.22,
    backgroundOverlay: 0,
    themeColor: '#2563eb',
  },
  gaussian_blur: {
    appearancePreset: 'gaussian_blur',
    cardOpacity: 0.58,
    cardBlur: 18,
    cardRadius: 24,
    borderStrength: 0.34,
    shadowStrength: 0.34,
    backgroundOverlay: 0.08,
    themeColor: '#6366f1',
  },
}

const appearancePresetOptions: Array<{ value: AppearancePreset; label: string }> = [
  { value: 'default', label: '默认主题' },
  { value: 'gaussian_blur', label: '高斯模糊主题' },
]

const fallbackLogoUrl = 'https://cdn.jsdelivr.net/gh/shuijiao1/Fly@main/ID-128.png'
const maxSettingsCustomCodeLength = 60000

function backgroundImageValue(url: string): string {
  return `url("${url.replaceAll('"', '%22')}")`
}

function clampNumber(value: number, min: number, max: number): number {
  return Number.isFinite(value) ? Math.min(max, Math.max(min, value)) : min
}

function appearanceValuesForSettings(settings: AdminSettings): AppearanceValues {
  const preset = settings.appearancePreset === 'gaussian_blur' ? 'gaussian_blur' : 'default'
  const fallback = appearancePresets[preset]
  return {
    appearancePreset: preset,
    cardOpacity: clampNumber(settings.cardOpacity ?? fallback.cardOpacity, 0.2, 1),
    cardBlur: clampNumber(settings.cardBlur ?? fallback.cardBlur, 0, 40),
    cardRadius: clampNumber(settings.cardRadius ?? fallback.cardRadius, 8, 36),
    borderStrength: clampNumber(settings.borderStrength ?? fallback.borderStrength, 0, 1),
    shadowStrength: clampNumber(settings.shadowStrength ?? fallback.shadowStrength, 0, 1),
    backgroundOverlay: clampNumber(settings.backgroundOverlay ?? fallback.backgroundOverlay, 0, 0.8),
    themeColor: /^#[0-9a-fA-F]{6}$/.test(settings.themeColor ?? '') ? settings.themeColor : fallback.themeColor,
  }
}

function hexToRgb(value: string): { r: number; g: number; b: number } {
  const normalized = /^#[0-9a-fA-F]{6}$/.test(value) ? value.slice(1) : '2563eb'
  return {
    r: Number.parseInt(normalized.slice(0, 2), 16),
    g: Number.parseInt(normalized.slice(2, 4), 16),
    b: Number.parseInt(normalized.slice(4, 6), 16),
  }
}

function rgbaFromHex(value: string, alpha: number): string {
  const { r, g, b } = hexToRgb(value)
  return `rgba(${r}, ${g}, ${b}, ${alpha.toFixed(3)})`
}

function storedThemeOverride(): AdminTheme | null {
  if (typeof window === 'undefined') return null
  const value = window.localStorage.getItem('zeno_theme_override')
  return value === 'system' || value === 'light' || value === 'dark' ? value : null
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
  const hasDedicatedMobileBackground = mobileBackgroundUrl !== ''
  const appearance = appearanceValuesForSettings(settings)
  const resolved = resolvedTheme(settings.theme)
  const themeColor = appearance.themeColor
  const themeRgb = hexToRgb(themeColor)
  const cardOpacity = appearance.cardOpacity
  const surfaceBase = resolved === 'dark' ? '15, 23, 42' : '255, 255, 255'
  const shadowBase = resolved === 'dark' ? '0, 0, 0' : '15, 23, 42'
  const shadowAlpha = 0.04 + appearance.shadowStrength * (resolved === 'dark' ? 0.44 : 0.22)
  const backgroundOverlayBase = resolved === 'dark' ? '0, 0, 0' : '255, 255, 255'
  return {
    '--zeno-desktop-background-image': desktopBackgroundUrl === '' ? 'none' : backgroundImageValue(desktopBackgroundUrl),
    '--zeno-mobile-background-image': hasDedicatedMobileBackground ? backgroundImageValue(mobileBackgroundUrl) : (desktopBackgroundUrl === '' ? 'none' : backgroundImageValue(desktopBackgroundUrl)),
    '--zeno-mobile-background-size': hasDedicatedMobileBackground ? 'contain' : 'cover',
    '--blue': themeColor,
    '--border': rgbaFromHex(themeColor, appearance.borderStrength),
    '--metric-shadow': rgbaFromHex(themeColor, Math.max(0.06, appearance.shadowStrength * 0.22)),
    '--surface-strong': `rgba(${surfaceBase}, ${cardOpacity.toFixed(3)})`,
    '--surface': `rgba(${surfaceBase}, ${Math.max(0.16, cardOpacity - 0.1).toFixed(3)})`,
    '--surface-soft': `rgba(${surfaceBase}, ${Math.max(0.12, cardOpacity - 0.34).toFixed(3)})`,
    '--secondary': `rgba(${surfaceBase}, ${Math.max(0.16, cardOpacity - 0.26).toFixed(3)})`,
    '--metric-bg': `rgba(${surfaceBase}, ${Math.max(0.18, cardOpacity - 0.2).toFixed(3)})`,
    '--field-bg': `rgba(${surfaceBase}, ${Math.max(0.18, cardOpacity - 0.14).toFixed(3)})`,
    '--control-bg': `rgba(${surfaceBase}, ${Math.max(0.18, cardOpacity - 0.1).toFixed(3)})`,
    '--radius-panel': `${appearance.cardRadius}px`,
    '--radius-card': `${Math.max(10, appearance.cardRadius - 4)}px`,
    '--radius-field': `${Math.max(8, appearance.cardRadius - 8)}px`,
    '--zeno-card-blur': `${appearance.cardBlur}px`,
    '--zeno-card-highlight': resolved === 'dark' ? `rgba(255, 255, 255, ${Math.min(0.18, 0.04 + appearance.shadowStrength * 0.12).toFixed(3)})` : `rgba(255, 255, 255, ${Math.min(0.9, 0.28 + cardOpacity * 0.42).toFixed(3)})`,
    '--zeno-card-shadow': `0 10px 26px -24px rgba(${shadowBase}, ${shadowAlpha.toFixed(3)}), 0 1px 2px rgba(${shadowBase}, ${(0.02 + appearance.shadowStrength * 0.05).toFixed(3)})`,
    '--zeno-background-overlay-color': `rgba(${backgroundOverlayBase}, ${appearance.backgroundOverlay.toFixed(3)})`,
    '--zeno-theme-rgb': `${themeRgb.r}, ${themeRgb.g}, ${themeRgb.b}`,
    backgroundSize: 'cover',
    backgroundAttachment: 'fixed',
  } as CSSProperties
}

export function documentBrandingForSettings(settings: AdminSettings) {
  const siteTitle = (settings.siteTitle || defaultSettings.siteTitle).trim() || defaultSettings.siteTitle
  const logoUrl = (settings.logoUrl || defaultSettings.logoUrl).trim() || defaultSettings.logoUrl
  return { title: siteTitle, iconHref: logoUrl }
}

export function orderHomeNodes(nodes: HomeCardNode[]): HomeCardNode[] {
  return nodes.map((node, index) => ({ node, index }))
    .sort((left, right) => {
      const leftOffline = left.node.status === 'online' ? 0 : 1
      const rightOffline = right.node.status === 'online' ? 0 : 1
      if (leftOffline !== rightOffline) return leftOffline - rightOffline
      return left.index - right.index
    })
    .map((entry) => entry.node)
}

export function applyDocumentBranding(settings: AdminSettings) {
  if (typeof document === 'undefined') return
  const branding = documentBrandingForSettings(settings)
  document.title = branding.title
  let icon = document.head.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!icon) {
    icon = document.createElement('link')
    icon.rel = 'icon'
    document.head.appendChild(icon)
  }
  icon.href = branding.iconHref
}

export function App() {
  const initialSummary = loadStoredSummary()
  const [state, setState] = useState<LoadState>(() => {
    return initialSummary ? { kind: 'ready', data: initialSummary.data, storedAt: initialSummary.storedAt, stale: initialSummary.stale } : { kind: 'loading' }
  })
  const [route, setRoute] = useState<DashboardRoute>(() => parseDashboardRoute(window.location.pathname))
  const [nodeLatencyRange, setNodeLatencyRange] = useState('1d')
  const [serviceLatencyRange, setServiceLatencyRange] = useState('1h')
  const [stateRange, setStateRange] = useState('1h')
  const [latencyState, setLatencyState] = useState<LatencyLoadState>({ kind: 'idle' })
  const [stateHistoryState, setStateHistoryState] = useState<StateHistoryLoadState>({ kind: 'idle' })
  const [serviceLatencyState, setServiceLatencyState] = useState<ServiceLatencyLoadState>({ kind: 'idle' })
  const nodeLatencyCacheRef = useRef(new DetailMemoryCache<NodeLatencyData>())
  const nodeStateCacheRef = useRef(new DetailMemoryCache<NodeStateData>())
  const serviceLatencyCacheRef = useRef(new DetailMemoryCache<ServiceLatencyData>())
  const summaryRef = useRef<SummaryData | null>(initialSummary?.data ?? null)
  const homeRealtimeMountedAtRef = useRef(monotonicNowMs())
  const homeRealtimeLastUpdatedAtRef = useRef<number | null>(null)
  const adminMutationEpochRef = useRef(createMutationEpoch())
  const adminSessionProbeRef = useRef(0)
  const [homeRealtimeSnapshot, setHomeRealtimeSnapshot] = useState<HomeRealtimeSnapshot | null>(() => state.kind === 'ready' ? homeRealtimeSnapshotForNodes(state.data.nodes) : null)
  const [adminToken, setAdminToken] = useState(loadStoredAdminToken)
  const [adminAuthState, setAdminAuthState] = useState<AdminAuthState>({ kind: 'idle' })
  const [adminState, setAdminState] = useState<AdminLoadState>({ kind: 'idle' })
  const [showAdminLoading, setShowAdminLoading] = useState(false)
  const [settings, setSettings] = useState<AdminSettings>(defaultSettings)
  const [settingsReady, setSettingsReady] = useState(false)
  const [backgroundAssetsReady, setBackgroundAssetsReady] = useState(false)
  const [themeOverride, setThemeOverride] = useState<AdminTheme | null>(() => storedThemeOverride())
  const [backgroundEnabled, setBackgroundEnabled] = useState(() => storedBackgroundEnabled())
  const backgroundEnabledRef = useRef(backgroundEnabled)

  useEffect(() => {
    let cancelled = false
    const probe = ++adminSessionProbeRef.current
    // HttpOnly cookies cannot be inspected by JavaScript. Probe the account
    // endpoint once so a secure cookie session survives a page refresh without
    // ever copying its replayable token into JS state or browser storage.
    fetchAdminAccount(adminCookieSessionMarker)
      .then(() => {
        if (cancelled || probe !== adminSessionProbeRef.current) return
        rememberAdminToken()
        setAdminToken(adminCookieSessionMarker)
      })
      .catch(() => {})
    return () => { cancelled = true }
  }, [])

  const clearAdminSession = (identity?: AdminTokenIdentity): boolean => {
    if (identity) {
      if (!clearStoredAdminTokenIfCurrent(identity)) return false
      setAdminToken((current) => current === identity.token ? '' : current)
    } else {
      clearStoredAdminToken()
      setAdminToken('')
    }
    setAdminState({ kind: 'idle' })
    return true
  }

  const expireAdminSession = (identity: AdminTokenIdentity): boolean => {
    if (!clearAdminSession(identity)) return false
    setAdminAuthState({ kind: 'error', message: '登录已过期，请重新登录。' })
    return true
  }

  useEffect(() => {
    let cancelled = false
    const settingsSnapshot = adminMutationEpochRef.current.snapshot()
    fetchPublicSettings()
      .then((loadedSettings) => {
        if (!cancelled) {
          if (adminMutationEpochRef.current.isCurrent(settingsSnapshot)) setSettings(loadedSettings)
          setSettingsReady(true)
        }
      })
      .catch(() => {
        if (!cancelled) setSettingsReady(true)
      })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    applyDocumentBranding(settings)
  }, [settings.siteTitle, settings.logoUrl])

  useEffect(() => {
    applyCustomCode(settings)
  }, [settings.customCode])

  useEffect(() => {
    if (!settingsReady || typeof Image === 'undefined') return undefined
    const urls = [...new Set([settings.desktopBackgroundUrl || settings.backgroundUrl, settings.mobileBackgroundUrl].map((value) => value.trim()).filter(Boolean))]
    if (urls.length === 0) {
      setBackgroundAssetsReady(true)
      return undefined
    }
    let active = true
    let remaining = urls.length
    const timers: number[] = []
    setBackgroundAssetsReady(false)
    const images = urls.map((url) => {
      const image = new Image()
      image.decoding = 'async'
      let settled = false
      const finish = () => {
        if (settled) return
        settled = true
        window.clearTimeout(timeoutID)
        remaining -= 1
        if (active && remaining === 0) setBackgroundAssetsReady(true)
      }
      const timeoutID = window.setTimeout(finish, 8000)
      timers.push(timeoutID)
      image.onload = finish
      image.onerror = finish
      image.src = url
      if (image.complete) queueMicrotask(finish)
      return image
    })
    return () => {
      active = false
      timers.forEach((timerID) => window.clearTimeout(timerID))
      images.forEach((image) => {
        image.onload = null
        image.onerror = null
      })
    }
  }, [settingsReady, settings.backgroundUrl, settings.desktopBackgroundUrl, settings.mobileBackgroundUrl])

  useEffect(() => {
    let cancelled = false
    const refreshHomeRealtimeSnapshot = (data: SummaryData) => {
      const now = monotonicNowMs()
      if (!shouldRefreshHomeRealtimeSnapshot(homeRealtimeLastUpdatedAtRef.current, now, homeRealtimeMountedAtRef.current)) return
      homeRealtimeLastUpdatedAtRef.current = now
      setHomeRealtimeSnapshot(homeRealtimeSnapshotForNodes(data.nodes))
    }
    const applySummaryData = (data: SummaryData) => {
      const storedAt = Date.now()
      rememberSummary(data, storedAt)
      summaryRef.current = data
      if (!cancelled) {
        refreshHomeRealtimeSnapshot(data)
        setState({ kind: 'ready', data, storedAt, stale: false })
      }
    }
    const stopLiveSummary = startResilientLiveData<SummaryData>({
      subscribe: subscribeSummary,
      fetch: fetchSummary,
      applyData: applySummaryData,
      initialFallbackDelayMs: detailHttpFallbackDelayMs,
      onError: (error) => {
        if (cancelled) return
        const message = error instanceof Error ? error.message : 'summary request failed'
        setState((current) => (current.kind === 'ready' ? { ...current, stale: true } : { kind: 'error', message }))
      },
    })
    return () => {
      cancelled = true
      stopLiveSummary()
    }
  }, [])

  useEffect(() => {
    const handlePopState = () => {
      blurActiveElement()
      setRoute(parseDashboardRoute(window.location.pathname))
    }
    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
  }, [])

  useLayoutEffect(() => {
    document.documentElement.scrollTop = 0
    document.body.scrollTop = 0
    window.scrollTo({ left: 0, top: 0, behavior: 'auto' })
  }, [route.kind, route.kind === 'node' ? route.nodeId : route.kind === 'service' ? route.targetId : ''])

  useEffect(() => {
    const hasToken = adminToken !== ''
    const nextNodeRange = coerceHistoryRange(nodeLatencyRange, hasToken, '1d')
    const nextStateRange = coerceHistoryRange(stateRange, hasToken, '1h')
    const nextServiceRange = coerceHistoryRange(serviceLatencyRange, hasToken, '1h')
    if (nextNodeRange !== nodeLatencyRange) setNodeLatencyRange(nextNodeRange)
    if (nextStateRange !== stateRange) setStateRange(nextStateRange)
    if (nextServiceRange !== serviceLatencyRange) setServiceLatencyRange(nextServiceRange)
  }, [adminToken, nodeLatencyRange, stateRange, serviceLatencyRange])

  useEffect(() => {
    if (route.kind !== 'node') {
      setLatencyState({ kind: 'idle' })
      return
    }

    let cancelled = false
    const cacheKey = `${route.nodeId}:${nodeLatencyRange}`
    const memoryCached = nodeLatencyCacheRef.current.getCached(cacheKey)
    const sessionCached = memoryCached ? null : loadCachedDetailData(nodeLatencyCachePrefix, route.nodeId, nodeLatencyRange, validateNodeLatencyData)
    const cached = memoryCached?.data ?? sessionCached?.data ?? null
    const seeded = cached ?? seedNodeLatencyFromSummary(summaryRef.current, route.nodeId, nodeLatencyRange)
    if (sessionCached) nodeLatencyCacheRef.current.set(cacheKey, sessionCached.data, sessionCached.storedAt)
    if (seeded) {
      setLatencyState({
        kind: 'ready',
        data: seeded,
        freshness: sessionCached ? { stale: sessionCached.stale, storedAt: sessionCached.storedAt, source: 'cache' } : memoryCached ? { stale: memoryCached.stale, storedAt: memoryCached.storedAt, source: 'cache' } : { stale: true, source: 'summary' },
      })
    } else {
      setLatencyState((current) => (current.kind === 'ready' && current.data.nodeId === route.nodeId ? current : { kind: 'loading' }))
    }
    const applyLatencyData = (data: NodeLatencyData, source: 'http' | 'ws') => {
      nodeLatencyCacheRef.current.set(cacheKey, data)
      rememberDetailData(nodeLatencyCachePrefix, route.nodeId, nodeLatencyRange, data)
      if (!cancelled) setLatencyState({ kind: 'ready', data, freshness: { stale: false, storedAt: Date.now(), source } })
    }
    const useLiveStream = !rangeRequiresAdmin(nodeLatencyRange)
    const requestToken = useLiveStream ? undefined : adminToken
    const requestTokenIdentity = requestToken ? captureAdminTokenIdentity(requestToken) : null
    const stopLatencyStream = startResilientLiveData<NodeLatencyData>({
      subscribe: useLiveStream ? (onData, onError, onStatus) => subscribeNodeLatency(route.nodeId, nodeLatencyRange, onData, onError, onStatus) : null,
      fetch: (signal) => fetchNodeLatency(route.nodeId, nodeLatencyRange, requestToken, signal),
      applyData: applyLatencyData,
      initialFallbackDelayMs: detailHttpFallbackDelayMs,
      onError: (error) => {
        if (cancelled) return
        const unauthorized = isHTTPUnauthorizedError(error)
        if (unauthorized && requestTokenIdentity && !expireAdminSession(requestTokenIdentity)) return
        setLatencyState((current) => (current.kind === 'ready'
          ? { ...current, freshness: { ...current.freshness, stale: true } }
          : { kind: 'error', message: unauthorized ? '登录已过期，请重新登录。' : error instanceof Error ? error.message : 'latency request failed' }))
      },
    })
    return () => {
      cancelled = true
      stopLatencyStream()
    }
  }, [route, nodeLatencyRange, adminToken])

  useEffect(() => {
    if (route.kind !== 'node') {
      setStateHistoryState({ kind: 'idle' })
      return
    }

    let cancelled = false
    const cacheKey = `${route.nodeId}:${stateRange}`
    const memoryCached = nodeStateCacheRef.current.getCached(cacheKey)
    const sessionCached = memoryCached ? null : loadCachedDetailData(nodeStateCachePrefix, route.nodeId, stateRange, validateNodeStateData)
    const cached = memoryCached?.data ?? sessionCached?.data ?? null
    const seeded = cached ?? seedNodeStateFromSummary(summaryRef.current, route.nodeId, stateRange)
    if (sessionCached) nodeStateCacheRef.current.set(cacheKey, sessionCached.data, sessionCached.storedAt)
    if (seeded) {
      setStateHistoryState({
        kind: 'ready',
        data: seeded,
        freshness: sessionCached ? { stale: sessionCached.stale, storedAt: sessionCached.storedAt, source: 'cache' } : memoryCached ? { stale: memoryCached.stale, storedAt: memoryCached.storedAt, source: 'cache' } : { stale: true, source: 'summary' },
      })
    } else {
      setStateHistoryState({ kind: 'loading' })
    }
    const applyStateData = (data: NodeStateData, source: 'http' | 'ws') => {
      nodeStateCacheRef.current.set(cacheKey, data)
      rememberDetailData(nodeStateCachePrefix, route.nodeId, stateRange, data)
      if (!cancelled) setStateHistoryState({ kind: 'ready', data, freshness: { stale: false, storedAt: Date.now(), source } })
    }
    const useLiveStream = !rangeRequiresAdmin(stateRange)
    const requestToken = useLiveStream ? undefined : adminToken
    const requestTokenIdentity = requestToken ? captureAdminTokenIdentity(requestToken) : null
    const stopStateStream = startResilientLiveData<NodeStateData>({
      subscribe: useLiveStream ? (onData, onError, onStatus) => subscribeNodeState(route.nodeId, stateRange, onData, onError, onStatus) : null,
      fetch: (signal) => fetchNodeState(route.nodeId, stateRange, requestToken, signal),
      applyData: applyStateData,
      initialFallbackDelayMs: detailHttpFallbackDelayMs,
      onError: (error) => {
        if (cancelled) return
        const unauthorized = isHTTPUnauthorizedError(error)
        if (unauthorized && requestTokenIdentity && !expireAdminSession(requestTokenIdentity)) return
        setStateHistoryState((current) => (current.kind === 'ready'
          ? { ...current, freshness: { ...current.freshness, stale: true } }
          : { kind: 'error', message: unauthorized ? '登录已过期，请重新登录。' : error instanceof Error ? error.message : 'state request failed' }))
      },
    })
    return () => {
      cancelled = true
      stopStateStream()
    }
  }, [route, stateRange, adminToken])

  useEffect(() => {
    if (state.kind !== 'ready' || route.kind !== 'node') return
    const latencySeed = seedNodeLatencyFromSummary(state.data, route.nodeId, nodeLatencyRange)
    if (latencySeed) {
      setLatencyState((current) => (current.kind === 'loading' || current.kind === 'idle' ? { kind: 'ready', data: latencySeed, freshness: { stale: true, source: 'summary' } } : current))
    }
    const stateSeed = seedNodeStateFromSummary(state.data, route.nodeId, stateRange)
    if (stateSeed) {
      setStateHistoryState((current) => (current.kind === 'loading' || current.kind === 'idle' ? { kind: 'ready', data: stateSeed, freshness: { stale: true, source: 'summary' } } : current))
    }
  }, [state, route, nodeLatencyRange, stateRange])

  useEffect(() => {
    if (route.kind !== 'service') {
      setServiceLatencyState({ kind: 'idle' })
      return
    }

    let cancelled = false
    const cacheKey = `${route.targetId}:${serviceLatencyRange}`
    const memoryCached = serviceLatencyCacheRef.current.getCached(cacheKey)
    const sessionCached = memoryCached ? null : loadCachedDetailData(serviceLatencyCachePrefix, route.targetId, serviceLatencyRange, validateServiceLatencyData)
    const cached = memoryCached?.data ?? sessionCached?.data ?? null
    if (cached) {
      if (sessionCached) serviceLatencyCacheRef.current.set(cacheKey, sessionCached.data, sessionCached.storedAt)
      setServiceLatencyState({
        kind: 'ready',
        data: cached,
        freshness: sessionCached ? { stale: sessionCached.stale, storedAt: sessionCached.storedAt, source: 'cache' } : { stale: memoryCached?.stale ?? false, storedAt: memoryCached?.storedAt, source: 'cache' },
      })
    } else {
      setServiceLatencyState({ kind: 'loading' })
    }
    const applyServiceLatencyData = (data: ServiceLatencyData, source: 'http' | 'ws') => {
      serviceLatencyCacheRef.current.set(cacheKey, data)
      rememberDetailData(serviceLatencyCachePrefix, route.targetId, serviceLatencyRange, data)
      if (!cancelled) setServiceLatencyState({ kind: 'ready', data, freshness: { stale: false, storedAt: Date.now(), source } })
    }
    const useLiveStream = !rangeRequiresAdmin(serviceLatencyRange)
    const requestToken = useLiveStream ? undefined : adminToken
    const requestTokenIdentity = requestToken ? captureAdminTokenIdentity(requestToken) : null
    const stopServiceLatencyStream = startResilientLiveData<ServiceLatencyData>({
      subscribe: useLiveStream ? (onData, onError, onStatus) => subscribeServiceLatency(route.targetId, serviceLatencyRange, onData, onError, onStatus) : null,
      fetch: (signal) => fetchServiceLatency(route.targetId, serviceLatencyRange, requestToken, signal),
      applyData: applyServiceLatencyData,
      initialFallbackDelayMs: detailHttpFallbackDelayMs,
      onError: (error) => {
        if (cancelled) return
        const unauthorized = isHTTPUnauthorizedError(error)
        if (unauthorized && requestTokenIdentity && !expireAdminSession(requestTokenIdentity)) return
        setServiceLatencyState((current) => (current.kind === 'ready'
          ? { ...current, freshness: { ...current.freshness, stale: true } }
          : { kind: 'error', message: unauthorized ? '登录已过期，请重新登录。' : error instanceof Error ? error.message : 'service latency request failed' }))
      },
    })
    return () => {
      cancelled = true
      stopServiceLatencyStream()
    }
  }, [route, serviceLatencyRange, adminToken])

  useEffect(() => {
    if (adminState.kind !== 'loading') {
      setShowAdminLoading(false)
      return
    }
    const timer = window.setTimeout(() => setShowAdminLoading(true), 450)
    return () => window.clearTimeout(timer)
  }, [adminState.kind])

  useEffect(() => {
    if (route.kind !== 'admin') return
    if (adminToken === '') {
      setAdminState({ kind: 'idle' })
      return
    }

    let cancelled = false
    let loadedOnce = false
    const loadAdminNodes = async (signal?: AbortSignal) => {
      const mutationSnapshot = adminMutationEpochRef.current.snapshot()
      if (!adminMutationEpochRef.current.isCurrent(mutationSnapshot)) return
      const requestToken = adminToken
      const requestTokenIdentity = captureAdminTokenIdentity(requestToken)
      if (!loadedOnce) setAdminState({ kind: 'loading' })
      try {
        const nodesData = await fetchAdminNodes(requestToken, signal)
        loadedOnce = true
        if (cancelled || signal?.aborted || !adminMutationEpochRef.current.isCurrent(mutationSnapshot)) return
        setAdminState((current) => ({
          kind: 'ready',
          account: current.kind === 'ready' ? current.account : { username: 'admin' },
          nodes: nodesData.nodes,
          targets: current.kind === 'ready' ? current.targets : [],
          notificationChannels: current.kind === 'ready' ? current.notificationChannels : [],
          alertRules: current.kind === 'ready' ? current.alertRules : [],
        }))
        const results = await Promise.allSettled([
          fetchAdminSettings(requestToken, signal),
          fetchAdminAccount(requestToken, signal),
          fetchAdminProbeTargets(requestToken, signal),
          fetchAdminNotificationChannels(requestToken, signal),
          fetchAdminAlertRules(requestToken, signal),
        ])
        if (cancelled || signal?.aborted || !adminMutationEpochRef.current.isCurrent(mutationSnapshot)) return
        const [settingsResult, accountResult, targetsResult, channelsResult, alertRulesResult] = results
        const unauthorizedResult = results.find((result) => result.status === 'rejected' && isAdminUnauthorizedError(result.reason))
        if (unauthorizedResult) {
          expireAdminSession(requestTokenIdentity)
          return
        }
        if (settingsResult.status === 'fulfilled') setSettings(settingsResult.value)
        setAdminState((current) => current.kind === 'ready' ? {
          ...current,
          account: accountResult.status === 'fulfilled' ? accountResult.value : current.account,
          targets: targetsResult.status === 'fulfilled' ? targetsResult.value.targets : current.targets,
          notificationChannels: channelsResult.status === 'fulfilled' ? channelsResult.value.channels : current.notificationChannels,
          alertRules: alertRulesResult.status === 'fulfilled' ? alertRulesResult.value.rules : current.alertRules,
        } : current)
      } catch (error: unknown) {
        loadedOnce = true
        if (cancelled || signal?.aborted || !adminMutationEpochRef.current.isCurrent(mutationSnapshot) || (error instanceof DOMException && error.name === 'AbortError')) return
        if (isAdminUnauthorizedError(error)) {
          expireAdminSession(requestTokenIdentity)
          return
        }
        setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
      }
    }

    const stopRefresh = startLiveRefresh(loadAdminNodes, { immediate: true, timeoutMs: 10_000 })
    return () => {
      cancelled = true
      stopRefresh()
    }
  }, [route, adminToken])

  const submitAdminLogin = (username: string, password: string) => {
    const trimmedUsername = username.trim()
    const trimmedPassword = password.trim()
    if (trimmedUsername === '' || trimmedPassword === '') return
    adminSessionProbeRef.current += 1
    setAdminAuthState({ kind: 'loading' })
    loginAdmin(trimmedUsername, trimmedPassword)
      .then((session) => {
        rememberAdminToken(session.token)
        setAdminToken(session.token)
        setAdminAuthState({ kind: 'idle' })
      })
      .catch((error: unknown) => {
        setAdminAuthState({ kind: 'error', message: error instanceof Error ? error.message : '登录失败' })
      })
  }

  const clearAdminToken = () => {
    adminSessionProbeRef.current += 1
    if (adminToken === '') {
      clearAdminSession()
      setAdminAuthState({ kind: 'idle' })
      return
    }
    const finishMutation = adminMutationEpochRef.current.beginMutation()
    const requestToken = adminToken
    const requestTokenIdentity = captureAdminTokenIdentity(requestToken)
    setAdminAuthState({ kind: 'loading' })
    logoutAdmin(requestToken)
      .then(() => {
        if (clearAdminSession(requestTokenIdentity)) setAdminAuthState({ kind: 'idle' })
      })
      .catch((error: unknown) => {
        if (isAdminUnauthorizedError(error)) {
          expireAdminSession(requestTokenIdentity)
          return
        }
        setAdminAuthState({ kind: 'error', message: error instanceof Error ? `退出失败：${error.message}` : '退出失败' })
      })
      .finally(finishMutation)
  }

  const handleAdminRequestError = (error: unknown, requestTokenIdentity: AdminTokenIdentity) => {
    if (isAdminUnauthorizedError(error)) {
      expireAdminSession(requestTokenIdentity)
      return
    }
    setAdminState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
  }

  const runAdminMutation = <T, R = void>(operation: () => Promise<T>, applyResult: (value: T) => R): Promise<R> => {
    if (adminToken === '') return Promise.reject(new Error('missing admin token'))
    const finishMutation = adminMutationEpochRef.current.beginMutation()
    const requestTokenIdentity = captureAdminTokenIdentity(adminToken)
    return operation()
      .then((value) => applyResult(value))
      .catch((error: unknown) => {
        // Mutation forms surface their own errors. Keep the ready dashboard mounted so
        // a failed save/delete does not replace the form with the page-level error state.
        if (isAdminUnauthorizedError(error)) expireAdminSession(requestTokenIdentity)
        throw error
      })
      .finally(finishMutation)
  }

  const updateAdminAccountDetails = (username: string, currentPassword: string, newPassword: string): Promise<void> => runAdminMutation(
    () => updateAdminAccount(adminToken, username, currentPassword, newPassword),
    (session) => {
      rememberAdminToken(session.token)
      setAdminToken(session.token)
      setAdminState((current) => current.kind === 'ready' ? { ...current, account: { username: session.username } } : current)
    },
  )

  const createAdminNodeDetails = (input: AdminNodeCreateInput): Promise<AdminNode> => runAdminMutation(
    () => createAdminNode(adminToken, input),
    (createdNode) => {
      setAdminState((current) => {
        if (current.kind === 'ready') {
          return { ...current, nodes: sortAdminNodes([...current.nodes, createdNode]) }
        }
        return { kind: 'ready', account: { username: 'admin' }, nodes: [createdNode], targets: [], notificationChannels: [], alertRules: [] }
      })
      return createdNode
    },
  )

  const requestAdminInstallCommand = (nodeId: string): Promise<AdminNodeInstallCommand> => {
    if (adminToken === '') return Promise.reject(new Error('missing admin token'))
    const controllerURL = settings.agentControllerUrl.trim() || (typeof window === 'undefined' ? '' : window.location.origin)
    if (remoteInsecureAgentControllerURL(controllerURL) && typeof window !== 'undefined' && !window.confirm('当前 Agent 接入地址使用远程 HTTP，enrollment/runtime token 将以明文传输。仅应在可信隔离网络使用，确认继续生成安装命令？')) {
      return Promise.reject(new Error('已取消生成明文 HTTP 安装命令。'))
    }
    const requestToken = adminToken
    const requestTokenIdentity = captureAdminTokenIdentity(requestToken)
    return requestAdminNodeInstallCommand(requestToken, nodeId)
      .catch((error: unknown) => {
        if (isAdminUnauthorizedError(error)) expireAdminSession(requestTokenIdentity)
        throw error
      })
  }

  const updateAdminNodeDetails = (nodeId: string, input: AdminNodeUpdateInput): Promise<void> => runAdminMutation(
    () => updateAdminNode(adminToken, nodeId, input),
    (updatedNode) => {
      setAdminState((current) => {
        if (current.kind === 'ready') {
          const selectedTargetIds = input.probeTargetIds === undefined ? null : new Set(input.probeTargetIds)
          return {
            ...current,
            nodes: sortAdminNodes(current.nodes.map((node) => node.id === updatedNode.id ? updatedNode : node)),
            targets: selectedTargetIds === null ? current.targets : current.targets.map((target) => {
              const existing = target.assignments.find((assignment) => assignment.nodeId === updatedNode.id)
              const enabled = selectedTargetIds.has(target.id)
              const nextAssignment = { nodeId: updatedNode.id, nodeDisplayName: updatedNode.displayName, enabled }
              return {
                ...target,
                assignments: existing
                  ? target.assignments.map((assignment) => assignment.nodeId === updatedNode.id ? nextAssignment : assignment)
                  : enabled ? [...target.assignments, nextAssignment] : target.assignments,
              }
            }),
          }
        }
        return { kind: 'ready', account: { username: 'admin' }, nodes: [updatedNode], targets: [], notificationChannels: [], alertRules: [] }
      })
    },
  )

  const deleteAdminNodeDetails = (nodeId: string): Promise<void> => runAdminMutation(
    () => deleteAdminNode(adminToken, nodeId),
    () => {
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
    },
  )

  const createAdminProbeTargetDetails = (input: AdminProbeTargetInput): Promise<void> => runAdminMutation(
    () => createAdminProbeTarget(adminToken, input),
    (createdTarget) => {
      setAdminState((current) => {
        if (current.kind === 'ready') {
          return { ...current, targets: sortAdminProbeTargets([...current.targets, createdTarget]) }
        }
        return { kind: 'ready', account: { username: 'admin' }, nodes: [], targets: [createdTarget], notificationChannels: [], alertRules: [] }
      })
    },
  )

  const updateAdminProbeTargetDetails = (targetId: string, input: AdminProbeTargetUpdateInput): Promise<void> => runAdminMutation(
    () => updateAdminProbeTarget(adminToken, targetId, input),
    (updatedTarget) => {
      setAdminState((current) => {
        if (current.kind === 'ready') {
          return { ...current, targets: sortAdminProbeTargets(current.targets.map((target) => target.id === updatedTarget.id ? updatedTarget : target)) }
        }
        return { kind: 'ready', account: { username: 'admin' }, nodes: [], targets: [updatedTarget], notificationChannels: [], alertRules: [] }
      })
    },
  )

  const deleteAdminProbeTargetDetails = (targetId: string): Promise<void> => runAdminMutation(
    () => deleteAdminProbeTarget(adminToken, targetId),
    () => {
      setAdminState((current) => {
        if (current.kind !== 'ready') return current
        return { ...current, targets: current.targets.filter((target) => target.id !== targetId) }
      })
    },
  )

  const createAdminNotificationChannelDetails = (input: AdminNotificationChannelCreateInput): Promise<void> => runAdminMutation(
    () => createAdminNotificationChannel(adminToken, input),
    (createdChannel) => {
      setAdminState((current) => {
        if (current.kind === 'ready') {
          return { ...current, notificationChannels: [...current.notificationChannels, createdChannel] }
        }
        return { kind: 'ready', account: { username: 'admin' }, nodes: [], targets: [], notificationChannels: [createdChannel], alertRules: [] }
      })
    },
  )

  const updateAdminNotificationChannelDetails = (channelId: string, input: AdminNotificationChannelUpdateInput): Promise<void> => runAdminMutation(
    () => updateAdminNotificationChannel(adminToken, channelId, input),
    (updatedChannel) => {
      setAdminState((current) => {
        if (current.kind === 'ready') {
          return { ...current, notificationChannels: current.notificationChannels.map((channel) => channel.id === updatedChannel.id ? updatedChannel : channel) }
        }
        return { kind: 'ready', account: { username: 'admin' }, nodes: [], targets: [], notificationChannels: [updatedChannel], alertRules: [] }
      })
    },
  )

  const deleteAdminNotificationChannelDetails = (channelId: string): Promise<void> => runAdminMutation(
    () => deleteAdminNotificationChannel(adminToken, channelId),
    () => {
      setAdminState((current) => {
        if (current.kind !== 'ready') return current
        return { ...current, notificationChannels: current.notificationChannels.filter((channel) => channel.id !== channelId) }
      })
    },
  )

  const testAdminNotificationChannelDetails = (channelId: string) => {
    if (adminToken === '') return
    const requestToken = adminToken
    const requestTokenIdentity = captureAdminTokenIdentity(requestToken)
    testAdminNotificationChannel(requestToken, channelId)
      .then(() => {})
      .catch((error: unknown) => handleAdminRequestError(error, requestTokenIdentity))
  }

  const updateAdminAlertRuleDetails = (ruleId: string, input: AdminAlertRuleUpdateInput): Promise<void> => runAdminMutation(
    () => updateAdminAlertRule(adminToken, ruleId, input),
    (updatedRule) => {
      setAdminState((current) => {
        if (current.kind === 'ready') {
          return {
            ...current,
            alertRules: current.alertRules.map((rule) => rule.id === updatedRule.id ? updatedRule : rule),
          }
        }
        return { kind: 'ready', account: { username: 'admin' }, nodes: [], targets: [], notificationChannels: [], alertRules: [updatedRule] }
      })
    },
  )

  const updateAdminSettingsDetails = (input: AdminSettingsUpdateInput): Promise<void> => runAdminMutation(
    () => updateAdminSettings(adminToken, input),
    (updatedSettings) => setSettings(updatedSettings),
  )

  const navigateHome = () => {
    blurActiveElement()
    window.history.pushState(null, '', '/')
    setRoute({ kind: 'home' })
  }

  const navigateAdmin = () => {
    blurActiveElement()
    window.history.pushState(null, '', '/dashboard')
    setRoute({ kind: 'admin' })
  }

  const navigateNode = (nodeId: string) => {
    blurActiveElement()
    window.history.pushState(null, '', nodePath(nodeId))
    setNodeLatencyRange('1d')
    setStateRange('1h')
    setRoute({ kind: 'node', nodeId })
  }

  const setThemeMode = (nextTheme: AdminTheme) => {
    window.localStorage.setItem('zeno_theme_override', nextTheme)
    setThemeOverride(nextTheme)
  }

  const toggleBackground = () => {
    const nextValue = !backgroundEnabledRef.current
    backgroundEnabledRef.current = nextValue
    window.localStorage.setItem('zeno_background_enabled', String(nextValue))
    setBackgroundEnabled(nextValue)
  }

  const effectiveSettings = settingsForChrome(settings, themeOverride, backgroundEnabled)
  const backgroundConfigured = (settings.desktopBackgroundUrl || settings.backgroundUrl || settings.mobileBackgroundUrl).trim() !== ''
  const backgroundToggle = settingsReady && backgroundConfigured && (!backgroundEnabled || backgroundAssetsReady) ? toggleBackground : undefined
  const nodes = state.kind === 'ready' ? state.data.nodes : []
  const homeRealtimeNodes = homeRealtimeSnapshot?.nodes ?? nodes
  const homeNodes = orderHomeNodes(homeRealtimeNodes)
  const services = state.kind === 'ready' ? state.data.services : []
  const selectedNode = route.kind === 'node' ? nodes.find((node) => node.id === route.nodeId) : undefined
  const selectedNodeLatencyPoints = latencyState.kind === 'ready' ? latencyState.data.points : summaryLatencyPoints(selectedNode)
  const selectedService = route.kind === 'service' ? services.find((service) => service.id === route.targetId) : undefined
  const totalCount = homeRealtimeNodes.length
  const onlineCount = homeRealtimeNodes.filter((node) => node.status === 'online').length
  const offlineCount = homeRealtimeNodes.filter((node) => node.status === 'offline').length
  const { totalUp, totalDown } = homeTrafficTotalsForNodes(homeRealtimeNodes)
  const currentRealtimeSnapshot = homeRealtimeSnapshot ?? homeRealtimeSnapshotForNodes(homeRealtimeNodes)
  const upSpeed = currentRealtimeSnapshot.upSpeed
  const downSpeed = currentRealtimeSnapshot.downSpeed
  const hasBackgroundImage = (effectiveSettings.desktopBackgroundUrl || effectiveSettings.backgroundUrl || effectiveSettings.mobileBackgroundUrl).trim() !== ''
  const hasAdminToken = adminToken !== ''
  const publicDataMayBeStale = state.kind === 'ready' && (
    state.stale === true
    || (route.kind === 'node' && (
      (latencyState.kind === 'ready' && latencyState.freshness?.stale === true)
      || (stateHistoryState.kind === 'ready' && stateHistoryState.freshness?.stale === true)
    ))
    || (route.kind === 'service' && serviceLatencyState.kind === 'ready' && serviceLatencyState.freshness?.stale === true)
  )

  return (
    <main className="kulin-shell" data-theme={effectiveSettings.theme} data-background={hasBackgroundImage ? 'on' : 'off'} style={shellStyleForSettings(effectiveSettings)}>
      {route.kind === 'admin' && (
        <AdminDashboard
          onHome={navigateHome}
          settings={settings}
          chromeSettings={effectiveSettings}
          hasAdminToken={hasAdminToken}
          authState={adminAuthState}
          adminState={adminState}
          showAdminLoading={showAdminLoading}
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
          onThemeChange={setThemeMode}
          onBackgroundToggle={backgroundToggle}
          backgroundEnabled={hasBackgroundImage}
        />
      )}

      {route.kind !== 'admin' && state.kind === 'loading' && <section className="state-panel">正在读取 Controller API…</section>}
      {route.kind !== 'admin' && state.kind === 'error' && <section className="state-panel is-error">API 读取失败：{state.message}</section>}
      {route.kind !== 'admin' && publicDataMayBeStale && <div className="data-stale-notice" role="status">数据可能已过期</div>}

      {state.kind === 'ready' && route.kind === 'node' && selectedNode && (
        <LatencyDetail
          node={selectedNode}
          points={selectedNodeLatencyPoints}
          statePoints={stateHistoryState.kind === 'ready' ? stateHistoryState.data.points : []}
          range={nodeLatencyRange}
          stateRange={stateRange}
          loading={latencyState.kind === 'loading'}
          error={latencyState.kind === 'error' ? latencyState.message : undefined}
          stateLoading={stateHistoryState.kind === 'loading'}
          stateError={stateHistoryState.kind === 'error' ? stateHistoryState.message : undefined}
          canUseExtendedRanges={hasAdminToken}
          onBack={navigateHome}
          onRangeChange={setNodeLatencyRange}
          onStateRangeChange={setStateRange}
          topHeader={<DashboardHeader settings={effectiveSettings} onHome={navigateHome} onAdmin={navigateAdmin} onThemeChange={setThemeMode} onBackgroundToggle={backgroundToggle} backgroundEnabled={hasBackgroundImage} />}
        />
      )}

      {state.kind === 'ready' && route.kind === 'node' && !selectedNode && (
        <section className="state-panel is-error">没有找到这台服务器：{route.nodeId}</section>
      )}

      {state.kind === 'ready' && route.kind === 'service' && (selectedService || serviceLatencyState.kind === 'ready') && (
        <ServiceDetail
          target={serviceLatencyState.kind === 'ready' ? serviceLatencyState.data.target : selectedService!}
          points={serviceLatencyState.kind === 'ready' ? serviceLatencyState.data.points : []}
          range={serviceLatencyRange}
          loading={serviceLatencyState.kind === 'loading'}
          error={serviceLatencyState.kind === 'error' ? serviceLatencyState.message : undefined}
          canUseExtendedRanges={hasAdminToken}
          onBack={navigateHome}
          onRangeChange={setServiceLatencyRange}
          topHeader={<DashboardHeader settings={effectiveSettings} onHome={navigateHome} onAdmin={navigateAdmin} onThemeChange={setThemeMode} onBackgroundToggle={backgroundToggle} backgroundEnabled={hasBackgroundImage} />}
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
            onThemeChange={setThemeMode}
            onBackgroundToggle={backgroundToggle}
            backgroundEnabled={hasBackgroundImage}
          />

          <section className="server-card-list" aria-label="server cards">
            {homeNodes.map((node) => <ServerCard key={node.id} node={node} onOpen={navigateNode} />)}
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
  onThemeChange?: (theme: AdminTheme) => void
  onBackgroundToggle?: () => void
  backgroundEnabled?: boolean
}

interface HomeTopPanelProps extends HomeOverviewPanelProps {
  onHome: () => void
  onAdmin: () => void
  onThemeChange?: (theme: AdminTheme) => void
  onBackgroundToggle?: () => void
  backgroundEnabled?: boolean
}

export function HomeTopPanel({ settings = defaultSettings, onHome, onAdmin, onThemeChange, onBackgroundToggle, backgroundEnabled = false, ...overview }: HomeTopPanelProps) {
  return (
    <section className="home-top-card" aria-label="homepage control panel">
      <DashboardHeader settings={settings} onHome={onHome} onAdmin={onAdmin} onThemeChange={onThemeChange} onBackgroundToggle={onBackgroundToggle} backgroundEnabled={backgroundEnabled} />
      <HomeOverviewPanel settings={settings} {...overview} />
    </section>
  )
}

function BrandLogo({ logoUrl, siteTitle }: { logoUrl?: string; siteTitle?: string }) {
  const source = (logoUrl ?? '').trim()
  const [currentSource, setCurrentSource] = useState(source)
  const [showLetterFallback, setShowLetterFallback] = useState(source === '')

  useEffect(() => {
    setCurrentSource(source)
    setShowLetterFallback(source === '')
  }, [source])

  if (showLetterFallback) {
    return <span className="brand-logo-fallback" role="img" aria-label={`${siteTitle || 'Zeno'} logo`}>Z</span>
  }

  return (
    <img
      src={currentSource}
      width="32"
      height="32"
      decoding="async"
      alt={`${siteTitle || 'Zeno'} logo`}
      onError={() => {
        if (currentSource !== defaultSettings.logoUrl) setCurrentSource(defaultSettings.logoUrl)
        else if (currentSource !== fallbackLogoUrl) setCurrentSource(fallbackLogoUrl)
        else setShowLetterFallback(true)
      }}
    />
  )
}

function DashboardHeader({ settings = defaultSettings, onHome, onAdmin, adminLabel = '后台', trailingAction, onThemeChange, onBackgroundToggle, backgroundEnabled = false }: DashboardHeaderProps) {
  const [themeMenuOpen, setThemeMenuOpen] = useState(false)
  const themeMenuRef = useRef<HTMLDivElement>(null)
  const themeMode = settings.theme
  const currentTheme = resolvedTheme(themeMode)
  const currentThemeLabel = headerThemeOptions.find((option) => option.value === themeMode)?.label ?? '跟随系统'
  const backgroundControlLabel = onBackgroundToggle
    ? (backgroundEnabled ? '关闭背景图' : '开启背景图')
    : (backgroundEnabled ? '背景图加载中' : '背景图未配置')

  useEffect(() => {
    if (!themeMenuOpen || typeof window === 'undefined') return undefined
    const handlePointerDown = (event: PointerEvent) => {
      if (themeMenuRef.current?.contains(event.target as Node)) return
      setThemeMenuOpen(false)
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setThemeMenuOpen(false)
    }
    window.addEventListener('pointerdown', handlePointerDown)
    window.addEventListener('keydown', handleKeyDown)
    return () => {
      window.removeEventListener('pointerdown', handlePointerDown)
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [themeMenuOpen])

  const selectTheme = (nextTheme: AdminTheme) => {
    onThemeChange?.(nextTheme)
    setThemeMenuOpen(false)
  }

  return (
    <header className="kulin-nav">
      <button className="brand" type="button" onClick={onHome}>
        <span className="brand-logo"><BrandLogo logoUrl={settings.logoUrl} siteTitle={settings.siteTitle} /></span>
        <span>{settings.siteTitle || 'Zeno'}</span>
      </button>
      <nav className="nav-actions" aria-label="dashboard actions">
        <button className="login-link" type="button" onClick={onAdmin}>{adminLabel}</button>
        <div className="theme-menu" ref={themeMenuRef}>
          <button className="nav-icon-button" type="button" aria-label={`主题：${currentThemeLabel}`} aria-haspopup="menu" aria-expanded={themeMenuOpen} onClick={() => setThemeMenuOpen((open) => !open)}>{themeMode === 'system' ? <MonitorIcon /> : currentTheme === 'dark' ? <MoonIcon /> : <SunIcon />}<span className="sr-only">切换深浅色</span></button>
          {themeMenuOpen && (
            <div className="theme-menu-popover" role="menu">
              {headerThemeOptions.map((option) => (
                <button key={option.value} type="button" role="menuitemradio" aria-checked={themeMode === option.value} data-active={themeMode === option.value} onClick={() => selectTheme(option.value)}>
                  <span>{option.label}</span>
                </button>
              ))}
            </div>
          )}
        </div>
        <button className={`nav-icon-button${backgroundEnabled ? ' is-solid' : ''}`} type="button" aria-label={backgroundControlLabel} aria-pressed={backgroundEnabled} disabled={!onBackgroundToggle} onClick={onBackgroundToggle}><ImageMinusIcon /><span className="sr-only">开关背景图</span></button>
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
  showAdminLoading?: boolean
  initialSection?: AdminSection
  onAdminLogin?: (username: string, password: string) => void
  onAdminTokenClear?: () => void
  onAdminAccountUpdate?: (username: string, currentPassword: string, newPassword: string) => Promise<void>
  onAdminNodeCreate?: (input: AdminNodeCreateInput) => Promise<AdminNode | void>
  onAdminNodeUpdate?: (nodeId: string, input: AdminNodeUpdateInput) => MaybePromise
  onAdminNodeDelete?: (nodeId: string) => MaybePromise
  onAdminInstallCommand?: (nodeId: string) => Promise<AdminNodeInstallCommand>
  onAdminProbeTargetCreate?: (input: AdminProbeTargetInput) => MaybePromise
  onAdminProbeTargetUpdate?: (targetId: string, input: AdminProbeTargetUpdateInput) => MaybePromise
  onAdminProbeTargetDelete?: (targetId: string) => MaybePromise
  onAdminNotificationChannelCreate?: (input: AdminNotificationChannelCreateInput) => MaybePromise
  onAdminNotificationChannelUpdate?: (channelId: string, input: AdminNotificationChannelUpdateInput) => MaybePromise
  onAdminNotificationChannelDelete?: (channelId: string) => MaybePromise
  onAdminNotificationChannelTest?: (channelId: string) => void
  onAdminAlertRuleUpdate?: (ruleId: string, input: AdminAlertRuleUpdateInput) => MaybePromise
  onAdminSettingsUpdate?: (input: AdminSettingsUpdateInput) => MaybePromise
  onThemeChange?: (theme: AdminTheme) => void
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
  showAdminLoading = false,
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
  onThemeChange,
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
      <section className={`home-top-card admin-panel${hasAdminToken ? '' : ' admin-panel--login'}`} aria-label="admin dashboard">
        <DashboardHeader
          settings={chromeSettings}
          onHome={onHome}
          onAdmin={onHome}
          adminLabel="前台"
          trailingAction={hasAdminToken ? <button className="nav-logout-button" type="button" onClick={onAdminTokenClear}>退出</button> : undefined}
          onThemeChange={onThemeChange}
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

            {adminState.kind === 'loading' && showAdminLoading && <div className="admin-state-card">加载中…</div>}
            {authState.kind === 'error' && <div className="admin-state-card is-error">{authState.message}</div>}
            {adminState.kind === 'error' && <div className="admin-state-card is-error">Admin API 读取失败：{adminState.message}</div>}

            {adminState.kind === 'ready' && activeSection === 'nodes' && (
              <AdminNodeSection
                nodes={adminState.nodes}
                targets={adminState.targets}
                onCreate={onAdminNodeCreate}
                onUpdate={onAdminNodeUpdate}
                onDelete={onAdminNodeDelete}
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
        <AdminFormSection title="修改密码">
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

function AdminSettingsSection({ settings, onUpdate }: { settings: AdminSettings; onUpdate: (input: AdminSettingsUpdateInput) => MaybePromise }) {
  const [settingsError, setSettingsError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [appearance, setAppearance] = useState<AppearanceValues>(() => appearanceValuesForSettings(settings))
  useEffect(() => {
    setAppearance(appearanceValuesForSettings(settings))
  }, [settings.appearancePreset, settings.cardOpacity, settings.cardBlur, settings.cardRadius, settings.borderStrength, settings.shadowStrength, settings.backgroundOverlay, settings.themeColor])
  const updateAppearance = (patch: Partial<AppearanceValues>) => setAppearance((current) => ({ ...current, ...patch }))
  const updateAppearancePreset = (value: string) => {
    const preset = value === 'gaussian_blur' ? 'gaussian_blur' : 'default'
    setAppearance(appearancePresets[preset])
  }
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
      appearancePreset: String(formData.get('appearance-preset') ?? appearance.appearancePreset) as AppearancePreset,
      cardOpacity: parseSettingsNumber(formData, 'card-opacity', appearance.cardOpacity),
      cardBlur: parseSettingsNumber(formData, 'card-blur', appearance.cardBlur),
      cardRadius: parseSettingsNumber(formData, 'card-radius', appearance.cardRadius),
      borderStrength: parseSettingsNumber(formData, 'border-strength', appearance.borderStrength),
      shadowStrength: parseSettingsNumber(formData, 'shadow-strength', appearance.shadowStrength),
      backgroundOverlay: parseSettingsNumber(formData, 'background-overlay', appearance.backgroundOverlay),
      themeColor: String(formData.get('theme-color') ?? appearance.themeColor).trim(),
      customCode: String(formData.get('custom-code') ?? '').trim(),
    }
    const validationError = validateAdminSettingsInput(input)
    if (validationError) {
      setSettingsError(validationError)
      return
    }
    setSettingsError(null)
    setSubmitting(true)
    Promise.resolve(onUpdate(input))
      .catch((error: unknown) => setSettingsError(error instanceof Error ? error.message : '设置保存失败'))
      .finally(() => setSubmitting(false))
  }

  return (
    <section className="admin-settings-section admin-workspace-panel" aria-label="admin settings">
      <header className="admin-section-heading">
        <div>
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
              <input name="logo-url" autoComplete="off" defaultValue={settings.logoUrl} placeholder="可留空" />
            </label>
          </div>
        </AdminFormSection>
        <AdminFormSection title="主题与背景">
          <div className="admin-form-grid">
            <AdminSegmentedField name="theme" label="主题" defaultValue={settings.theme} options={themeOptions} />
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
        <AdminFormSection title="外观样式">
          <div className="admin-appearance-layout">
            <div className="admin-appearance-main">
              <div className="admin-appearance-top">
                <AdminAppearancePresetCards value={appearance.appearancePreset} onChange={updateAppearancePreset} />
                <label className="admin-color-field">
                  <span>主题色</span>
                  <span className="admin-color-field__row">
                    <input name="theme-color" type="color" value={appearance.themeColor} onChange={(event) => updateAppearance({ themeColor: event.currentTarget.value })} />
                    <strong>{appearance.themeColor.toUpperCase()}</strong>
                  </span>
                </label>
              </div>
              <div className="admin-style-grid">
                <AdminStyleRangeField name="card-opacity" label="卡片透明度" value={appearance.cardOpacity} min={0.2} max={1} step={0.01} onChange={(value) => updateAppearance({ cardOpacity: value })} formatValue={(value) => `${Math.round(value * 100)}%`} />
                <AdminStyleRangeField name="card-blur" label="卡片模糊度" value={appearance.cardBlur} min={0} max={40} step={1} onChange={(value) => updateAppearance({ cardBlur: value })} formatValue={(value) => `${Math.round(value)}px`} />
                <AdminStyleRangeField name="card-radius" label="卡片圆角" value={appearance.cardRadius} min={8} max={36} step={1} onChange={(value) => updateAppearance({ cardRadius: value })} formatValue={(value) => `${Math.round(value)}px`} />
                <AdminStyleRangeField name="border-strength" label="边框强度" value={appearance.borderStrength} min={0} max={1} step={0.01} onChange={(value) => updateAppearance({ borderStrength: value })} formatValue={(value) => `${Math.round(value * 100)}%`} />
                <AdminStyleRangeField name="shadow-strength" label="阴影强度" value={appearance.shadowStrength} min={0} max={1} step={0.01} onChange={(value) => updateAppearance({ shadowStrength: value })} formatValue={(value) => `${Math.round(value * 100)}%`} />
                <AdminStyleRangeField name="background-overlay" label="背景遮罩" value={appearance.backgroundOverlay} min={0} max={0.8} step={0.01} onChange={(value) => updateAppearance({ backgroundOverlay: value })} formatValue={(value) => `${Math.round(value * 100)}%`} />
              </div>
            </div>
            <AdminAppearancePreview appearance={appearance} />
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
        <AdminFormSection title="自定义 CSS">
          <div className="admin-form-grid">
            <label className="admin-form-span-2">
              <span>自定义 CSS</span>
              <textarea className="admin-code-field" name="custom-code" defaultValue={settings.customCode} spellCheck={false} placeholder={'<style>\n.home-top-card { border-color: #2563eb; }\n</style>'} />
            </label>
          </div>
        </AdminFormSection>
        {settingsError && <p className="admin-install-error">{settingsError}</p>}
        <div className="admin-modal-actions">
          <button type="submit" disabled={submitting}>{submitting ? '保存中…' : '保存设置'}</button>
        </div>
      </form>
    </section>
  )
}

export function validateAdminSettingsInput(input: AdminSettingsUpdateInput): string | null {
  if (!validSettingsImageURL(input.logoUrl ?? '')) return '头像 / Logo URL 只能是 https:// 链接或 /assets/... 站内路径。'
  if (!validSettingsImageURL(input.desktopBackgroundUrl ?? input.backgroundUrl ?? '')) return '电脑端背景图 URL 只能是 https:// 链接或 /assets/... 站内路径。'
  if (!validSettingsImageURL(input.mobileBackgroundUrl ?? '')) return '手机端背景图 URL 只能是 https:// 链接或 /assets/... 站内路径。'
  if (!validAgentControllerURL(input.agentControllerUrl ?? '')) return 'Agent 接入 URL 必须使用 https://；loopback 或“直接 IP + 显式端口”可使用 http://，且不能包含用户名密码、query 或 fragment。'
  if (input.appearancePreset !== undefined && input.appearancePreset !== 'default' && input.appearancePreset !== 'gaussian_blur') return '外观模板无效。'
  if (!validSettingsNumber(input.cardOpacity, 0.2, 1)) return '卡片透明度无效。'
  if (!validSettingsNumber(input.cardBlur, 0, 40)) return '卡片模糊度无效。'
  if (!validSettingsNumber(input.cardRadius, 8, 36)) return '卡片圆角无效。'
  if (!validSettingsNumber(input.borderStrength, 0, 1)) return '边框强度无效。'
  if (!validSettingsNumber(input.shadowStrength, 0, 1)) return '阴影强度无效。'
  if (!validSettingsNumber(input.backgroundOverlay, 0, 0.8)) return '背景遮罩无效。'
  if (input.themeColor !== undefined && !/^#[0-9a-fA-F]{6}$/.test(input.themeColor)) return '主题色无效。'
  if (customCodeLength(input.customCode ?? '') > maxSettingsCustomCodeLength) return '自定义代码不能超过 60000 字。'
  return null
}

function validSettingsNumber(value: number | undefined, min: number, max: number): boolean {
  return value === undefined || (Number.isFinite(value) && value >= min && value <= max)
}

function customCodeLength(value: string): number {
  return Array.from(value.trim()).length
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
    const host = parsed.hostname.toLowerCase().replace(/^\[|\]$/g, '')
    const ipv4 = host.split('.')
    const validIPv4 = ipv4.length === 4 && ipv4.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255)
    const loopbackIPv4 = validIPv4 && ipv4[0] === '127'
    const loopback = host === 'localhost' || host === '::1' || loopbackIPv4
    const authority = trimmed.match(/^http:\/\/([^/?#]+)/i)?.[1] ?? ''
    const explicitPortMatch = authority.match(/^\[[^\]]+\]:(\d+)$/) ?? authority.match(/^[^:]+:(\d+)$/)
    const explicitPort = explicitPortMatch ? Number(explicitPortMatch[1]) : 0
    const directIPWithPort = (validIPv4 || host.includes(':')) && explicitPort >= 1 && explicitPort <= 65535
    return (parsed.protocol === 'https:' || (parsed.protocol === 'http:' && (loopback || directIPWithPort))) && parsed.hostname !== '' && parsed.username === '' && parsed.password === '' && parsed.search === '' && parsed.hash === ''
  } catch {
    return false
  }
}

export function remoteInsecureAgentControllerURL(value: string): boolean {
  try {
    const parsed = new URL(value.trim())
    if (parsed.protocol !== 'http:') return false
    const host = parsed.hostname.toLowerCase().replace(/^\[|\]$/g, '').replace(/\.$/, '')
    if (host === 'localhost' || host === '::1') return false
    const ipv4 = host.split('.')
    return !(ipv4.length === 4 && ipv4.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255) && ipv4[0] === '127')
  } catch {
    return false
  }
}

function AdminNodeSection({ nodes, targets, onCreate, onUpdate, onDelete, onInstallCommand }: { nodes: AdminNode[]; targets: AdminProbeTarget[]; onCreate: (input: AdminNodeCreateInput) => Promise<AdminNode | void>; onUpdate: (nodeId: string, input: AdminNodeUpdateInput) => MaybePromise; onDelete: (nodeId: string) => MaybePromise; onInstallCommand: (nodeId: string) => Promise<AdminNodeInstallCommand> }) {
  const [creatingNode, setCreatingNode] = useState(false)
  const [editingNodeId, setEditingNodeId] = useState<string | null>(null)
  const [sortingNodes, setSortingNodes] = useState(false)
  const editingNode = editingNodeId ? nodes.find((node) => node.id === editingNodeId) : undefined
  const orderedNodes = sortAdminNodes(nodes)
  const applyOrderPatches = (orderedNodes: AdminNode[]) => {
    const patches = buildAdminNodeOrderPatches(orderedNodes)
    return Promise.all(patches.map((patch) => Promise.resolve(onUpdate(patch.nodeId, { displayOrder: patch.displayOrder })))).then(() => undefined)
  }

  return (
    <section className="admin-node-section admin-workspace-panel" aria-label="admin node list">
      <header className="admin-section-heading">
        <div>
          <h3>服务器列表</h3>
        </div>
        <div className="admin-section-actions">
          <button className="admin-primary-action" type="button" onClick={() => setSortingNodes(true)}>服务器排序</button>
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
          onUpdate={onUpdate}
          onInstallCommand={onInstallCommand}
        />
      )}

      {sortingNodes && (
        <AdminNodeSortModal
          nodes={orderedNodes}
          onClose={() => setSortingNodes(false)}
          onSave={async (nextNodes) => {
            await applyOrderPatches(nextNodes)
            setSortingNodes(false)
          }}
        />
      )}
    </section>
  )
}

type AdminNodeOrderPatch = { nodeId: string; displayOrder: number }

function AdminNodeList({ nodes, onEdit, onDelete }: { nodes: AdminNode[]; onEdit: (nodeId: string) => void; onDelete: (nodeId: string) => MaybePromise }) {
  const [pendingDelete, setPendingDelete] = useState<AdminNode | null>(null)

  return (
    <>
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
            <strong className="admin-node-title"><ServerFlag countryCode={node.countryCode} className="admin-list-flag" /><span>{node.displayName}</span></strong>
          </div>
          <span data-label="公网 IP" className={`admin-ip-stack${node.publicIPv6 ? '' : ' is-single'}`}>
            {node.publicIPv4 && <span>{node.publicIPv4}</span>}
            {node.publicIPv6 && <span>{node.publicIPv6}</span>}
            {!node.publicIPv4 && !node.publicIPv6 && <span>—</span>}
          </span>
          <span data-label="Agent 版本">{node.agentVersion || '—'}</span>
          <div className="admin-row-actions admin-icon-actions">
            <button className="admin-row-action is-icon" type="button" aria-label={`编辑服务器 ${node.displayName}`} title="编辑服务器" onClick={() => onEdit(node.id)}><EditActionIcon /><span className="sr-only">编辑服务器</span></button>
            <button className="admin-row-action is-icon is-danger" type="button" aria-label={`删除服务器 ${node.displayName}`} title="删除服务器" onClick={() => setPendingDelete(node)}><TrashActionIcon /><span className="sr-only">删除服务器</span></button>
          </div>
        </article>
      ))}
    </div>
    {pendingDelete && (
      <AdminDeleteConfirmModal
        title="删除服务器"
        subjectName={pendingDelete.displayName}
        confirmLabel="删除服务器"
        onClose={() => setPendingDelete(null)}
        onConfirm={() => onDelete(pendingDelete.id)}
      />
    )}
    </>
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

function AdminNodeSortModal({ nodes, onSave, onClose }: { nodes: AdminNode[]; onSave: (nodes: AdminNode[]) => MaybePromise; onClose: () => void }) {
  const [orderedIds, setOrderedIds] = useState(() => nodes.map((node) => node.id))
  const [draggedNodeId, setDraggedNodeId] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
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
  const saveOrder = () => {
    setSubmitting(true)
    setFormError(null)
    Promise.resolve(onSave(orderedNodes))
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '保存失败'))
      .finally(() => setSubmitting(false))
  }

  return (
    <AdminModalLayer><div className="admin-modal-backdrop" role="presentation">
      <div className="admin-modal" role="dialog" aria-modal="true" aria-label="服务器排序">
        <header className="admin-modal-header">
          <div>
              <h3>服务器排序</h3>
          </div>
          <button className="admin-modal-close" type="button" aria-label="关闭" onClick={onClose}>×</button>
        </header>
        <div className="admin-modal-body">
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
            {formError && <span className="admin-inline-note admin-modal-action-note is-error">{formError}</span>}
            <button type="button" onClick={onClose}>取消</button>
            <button className="admin-primary-action" type="button" onClick={saveOrder} disabled={submitting}>{submitting ? '保存中…' : '保存排序'}</button>
          </div>
        </div>
      </div>
    </div></AdminModalLayer>
  )
}

type AgentInstallPlatform = 'linux' | 'macos' | 'windows'

type InstallCommandState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; command: string; commands: Partial<Record<AgentInstallPlatform, string>>; platform: AgentInstallPlatform | null }
  | { kind: 'error'; message: string }

type InstallNoticeState =
  | { kind: 'idle' }
  | { kind: 'ready'; message: string }
  | { kind: 'warning'; message: string }
  | { kind: 'error'; message: string }

const agentInstallPlatforms: Array<{ value: AgentInstallPlatform; label: string }> = [
  { value: 'linux', label: 'Linux' },
  { value: 'macos', label: 'macOS' },
  { value: 'windows', label: 'Windows' },
]

function installCommandForPlatform(state: InstallCommandState, platform: AgentInstallPlatform): string {
  if (state.kind !== 'ready') return ''
  return state.commands[platform] || state.command
}

function installCommandReady(result: AdminNodeInstallCommand): InstallCommandState {
  return {
    kind: 'ready',
    command: result.command,
    commands: { linux: result.command, ...result.commands },
    platform: null,
  }
}

function installPlatformMenuPosition(trigger: HTMLButtonElement | null): CSSProperties {
  if (typeof window === 'undefined' || !trigger) return {}
  const rect = trigger.getBoundingClientRect()
  const gap = 8
  const margin = 12
  const width = 184
  const height = 124
  const left = Math.min(Math.max(rect.left, margin), Math.max(margin, window.innerWidth - width - margin))
  const hasRoomBelow = rect.bottom + gap + height <= window.innerHeight - margin
  const top = hasRoomBelow ? rect.bottom + gap : Math.max(margin, rect.top - gap - height)
  return { left, top }
}

function AdminInstallPlatformPopover({ state, style, onSelect }: { state: InstallCommandState; style: CSSProperties; onSelect: (platform: AgentInstallPlatform) => void }) {
  if (state.kind !== 'ready') return null
  const popover = (
    <div className="admin-install-platforms" style={style} role="group" aria-label="选择 Agent 安装系统">
      {agentInstallPlatforms.map((platform) => (
        <button key={platform.value} type="button" data-active={state.platform === platform.value} onClick={() => onSelect(platform.value)}>{platform.label}</button>
      ))}
    </div>
  )
  return typeof document === 'undefined' ? popover : createPortal(popover, document.body)
}

function AdminNodeCreateModal({ onCreate, onInstallCommand, onClose }: { onCreate: (input: AdminNodeCreateInput) => Promise<AdminNode | void>; onInstallCommand: (nodeId: string) => Promise<AdminNodeInstallCommand>; onClose: () => void }) {
  const [createdNode, setCreatedNode] = useState<AdminNode | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [installCommandState, setInstallCommandState] = useState<InstallCommandState>({ kind: 'idle' })
  const [installCopyState, setInstallCopyState] = useState<InstallNoticeState>({ kind: 'idle' })
  const [installPlatformPickerOpen, setInstallPlatformPickerOpen] = useState(false)
  const [installPlatformMenuStyle, setInstallPlatformMenuStyle] = useState<CSSProperties>({})
  const installCopyButtonRef = useRef<HTMLButtonElement>(null)
  const formRef = useRef<HTMLFormElement>(null)

  const nodeInputFromForm = (form: HTMLFormElement): AdminNodeCreateInput | null => {
    const formData = new FormData(form)
    const displayName = String(formData.get('new-display-name') ?? '').trim()
    if (displayName === '') return null
    return {
      displayName,
      expiryDate: String(formData.get('new-expiry-date') ?? '').trim(),
      expiryPermanent: formData.get('new-expiry-permanent') === '1',
      billingCycle: String(formData.get('new-billing-cycle') ?? '').trim(),
      billingMode: String(formData.get('new-billing-mode') ?? 'both'),
      monthlyResetDay: parseMonthlyResetDay(String(formData.get('new-monthly-reset-day') ?? '')) ?? 1,
      monthlyQuotaBytes: parseQuota(String(formData.get('new-monthly-quota') ?? ''), String(formData.get('new-monthly-quota-unit') ?? 'GB')),
    }
  }

  const createNodeFromForm = (form: HTMLFormElement): Promise<AdminNode | null> => {
    const input = nodeInputFromForm(form)
    if (!input) {
      setFormError('请先填写服务器名称。')
      return Promise.resolve(null)
    }
    setSubmitting(true)
    setFormError(null)
    setInstallCopyState({ kind: 'idle' })
    return onCreate(input)
      .then((node) => {
        if (node) setCreatedNode(node)
        return node ?? null
      })
      .catch((error: unknown) => {
        setFormError(error instanceof Error ? error.message : '添加服务器失败')
        return null
      })
      .finally(() => setSubmitting(false))
  }

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    createNodeFromForm(event.currentTarget)
  }

  const requestInstallCommand = (openPickerAfterGenerate = false, node = createdNode) => {
    if (!node) return
    setInstallCommandState({ kind: 'loading' })
    setInstallCopyState({ kind: 'idle' })
    setInstallPlatformPickerOpen(false)
    onInstallCommand(node.id)
      .then((result) => {
        setInstallCommandState(installCommandReady(result))
        if (openPickerAfterGenerate) {
          setInstallPlatformPickerOpen(true)
          setInstallPlatformMenuStyle(installPlatformMenuPosition(installCopyButtonRef.current))
        }
      })
      .catch((error: unknown) => setInstallCommandState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const handleCopyInstallCommand = () => {
    if (installCommandState.kind === 'loading' || submitting) return
    if (!createdNode) return
    if (installCommandState.kind !== 'ready') {
      requestInstallCommand(true)
      return
    }
    setInstallPlatformPickerOpen(true)
    setInstallPlatformMenuStyle(installPlatformMenuPosition(installCopyButtonRef.current))
    setInstallCopyState({ kind: 'idle' })
  }

  const handleCopyInstallPlatform = (platform: AgentInstallPlatform) => {
    const command = installCommandForPlatform(installCommandState, platform)
    if (!command) return
    copyTextToClipboard(command)
      .then(() => {
        setInstallCommandState((current) => current.kind === 'ready' ? { ...current, platform } : current)
        setInstallPlatformPickerOpen(false)
        setInstallCopyState({ kind: 'ready', message: '安装命令已复制。' })
      })
      .catch((error: unknown) => setInstallCopyState({ kind: 'error', message: error instanceof Error ? error.message : '复制失败，请手动选中复制。' }))
  }

  return (
    <AdminModal title="添加服务器" onClose={onClose}>
      <form ref={formRef} className="admin-node-create-form admin-node-edit-form is-sectioned" aria-label="添加服务器" onSubmit={handleSubmit}>
        <AdminFormSection title="服务器名称">
          <div className="admin-form-grid">
            <label>
              <span>服务器名称</span>
              <input name="new-display-name" autoComplete="off" placeholder="New Server" disabled={Boolean(createdNode)} />
            </label>
          </div>
        </AdminFormSection>
        <AdminFormSection title="账单与流量">
          <div className="admin-billing-grid">
            <div className="admin-billing-row admin-billing-row--cycle">
              <AdminDateField className="admin-billing-control admin-billing-control--expiry" name="new-expiry-date" label="到期日" permanentLabel="设为永久" disabled={Boolean(createdNode)} />
              <label className="admin-billing-control admin-billing-control--reset">
                <span>月流量重置日</span>
                <input name="new-monthly-reset-day" type="number" min="1" max="31" step="1" defaultValue="1" disabled={Boolean(createdNode)} />
              </label>
              <AdminSegmentedField className="admin-billing-control admin-billing-control--cycle" name="new-billing-cycle" label="账单周期" defaultValue="月" options={billingCycleOptions} disabled={Boolean(createdNode)} />
            </div>
            <div className="admin-billing-row admin-billing-row--traffic">
              <AdminSegmentedField className="admin-billing-control admin-billing-control--mode" name="new-billing-mode" label="流量计费口径" defaultValue="both" options={billingModeOptions} disabled={Boolean(createdNode)} />
              <label className="admin-billing-control admin-billing-control--quota">
                <span>月配额</span>
                <input name="new-monthly-quota" type="number" min="0" step="0.01" disabled={Boolean(createdNode)} />
              </label>
              <AdminSegmentedField className="admin-billing-control admin-billing-control--unit" name="new-monthly-quota-unit" label="配额单位" defaultValue="GB" options={quotaUnitOptions} disabled={Boolean(createdNode)} />
            </div>
          </div>
        </AdminFormSection>
        {createdNode && (
          <AdminFormSection title="Agent 接入">
            <div className="admin-inline-actions admin-install-copy-row">
              <div className="admin-install-copy-menu">
                <button ref={installCopyButtonRef} className="admin-primary-action admin-install-copy-button" type="button" onClick={handleCopyInstallCommand} disabled={installCommandState.kind === 'loading'}>{installCommandState.kind === 'loading' ? '生成中…' : '复制安装命令'}</button>
                {installPlatformPickerOpen && <AdminInstallPlatformPopover state={installCommandState} style={installPlatformMenuStyle} onSelect={handleCopyInstallPlatform} />}
              </div>
              {installCopyState.kind === 'idle' && <span className="admin-inline-note is-success">已添加：{createdNode.displayName}</span>}
              {installCopyState.kind !== 'idle' && <span className={`admin-inline-note${installCopyState.kind === 'ready' ? ' is-success' : installCopyState.kind === 'warning' ? ' is-warning' : ' is-error'}`}>{installCopyState.message}</span>}
            </div>
            {installCommandState.kind === 'error' && <div className="admin-install-error">安装命令生成失败：{installCommandState.message}</div>}
          </AdminFormSection>
        )}
        <div className="admin-modal-actions">
          {formError && <span className="admin-inline-note admin-modal-action-note is-error">{formError}</span>}
          <button type="submit" disabled={submitting || Boolean(createdNode)}>{submitting ? '添加中…' : createdNode ? '服务器已添加' : '添加服务器'}</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminNodeEditModal({ node, targets, onUpdate, onInstallCommand, onClose }: { node: AdminNode; targets: AdminProbeTarget[]; onUpdate: (nodeId: string, input: AdminNodeUpdateInput) => MaybePromise; onInstallCommand: (nodeId: string) => Promise<AdminNodeInstallCommand>; onClose: () => void }) {
  const [installCommandState, setInstallCommandState] = useState<InstallCommandState>({ kind: 'idle' })
  const [installCopyState, setInstallCopyState] = useState<InstallNoticeState>({ kind: 'idle' })
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [installPlatformPickerOpen, setInstallPlatformPickerOpen] = useState(false)
  const [installPlatformMenuStyle, setInstallPlatformMenuStyle] = useState<CSSProperties>({})
  const installCopyButtonRef = useRef<HTMLButtonElement>(null)
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
    setSubmitting(true)
    setFormError(null)
    Promise.resolve(onUpdate(node.id, {
      displayName: displayName || node.displayName,
      homeProbeTargetId: selectedTargets.has(homeTargetId) ? homeTargetId : '',
      expiryDate: String(formData.get('expiry-date') ?? '').trim(),
      expiryPermanent: formData.get('expiry-permanent') === '1',
      billingCycle: String(formData.get('billing-cycle') ?? '').trim(),
      billingMode: String(formData.get('billing-mode') ?? node.billingMode),
      monthlyResetDay: parseMonthlyResetDay(String(formData.get('monthly-reset-day') ?? '')) ?? node.monthlyResetDay,
      monthlyQuotaBytes: parseQuota(String(formData.get('monthly-quota') ?? ''), String(formData.get('monthly-quota-unit') ?? quotaUnitForBytes(node.monthlyQuotaBytes))),
      probeTargetIds: [...selectedTargets],
    }))
      .then(() => onClose())
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '保存失败'))
      .finally(() => setSubmitting(false))
  }

  const requestInstallCommand = (openPickerAfterGenerate = false) => {
    setInstallCommandState({ kind: 'loading' })
    setInstallCopyState({ kind: 'idle' })
    setInstallPlatformPickerOpen(false)
    onInstallCommand(node.id)
      .then((result) => {
        setInstallCommandState(installCommandReady(result))
        if (openPickerAfterGenerate) {
          setInstallPlatformPickerOpen(true)
          setInstallPlatformMenuStyle(installPlatformMenuPosition(installCopyButtonRef.current))
        }
      })
      .catch((error: unknown) => setInstallCommandState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' }))
  }

  const handleCopyInstallCommand = () => {
    if (installCommandState.kind === 'loading') return
    if (installCommandState.kind !== 'ready') {
      requestInstallCommand(true)
      return
    }
    setInstallPlatformPickerOpen(true)
    setInstallPlatformMenuStyle(installPlatformMenuPosition(installCopyButtonRef.current))
    setInstallCopyState({ kind: 'idle' })
  }

  const handleCopyInstallPlatform = (platform: AgentInstallPlatform) => {
    const command = installCommandForPlatform(installCommandState, platform)
    if (!command) return
    copyTextToClipboard(command)
      .then(() => {
        setInstallCommandState((current) => current.kind === 'ready' ? { ...current, platform } : current)
        setInstallPlatformPickerOpen(false)
        setInstallCopyState({ kind: 'ready', message: '安装命令已复制。' })
      })
      .catch((error: unknown) => setInstallCopyState({ kind: 'error', message: error instanceof Error ? error.message : '复制失败，请手动选中复制。' }))
  }

  return (
    <AdminModal title={`编辑服务器 · ${node.displayName}`} onClose={onClose}>
      <form className="admin-node-edit-form is-sectioned" aria-label={`${node.displayName} 节点编辑`} onSubmit={handleSubmit}>
        <AdminFormSection title="服务器名称">
          <div className="admin-form-grid">
            <label className="admin-label-without-caption">
              <input name="display-name" defaultValue={node.displayName} autoComplete="off" aria-label="服务器名称" />
            </label>
          </div>
        </AdminFormSection>
        <AdminFormSection title="关联延迟监控">
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
                <label className="admin-home-monitor-radio">
                  <input
                    type="radio"
                    name={`home-monitor-${node.id}`}
                    checked={homeTargetId === option.value}
                    onChange={() => {
                      if (!checked) updateSelectedTargetIds([...selectedTargetIds, option.value])
                      setHomeTargetId(option.value)
                    }}
                  />
                  <span>首页展示</span>
                </label>
              )}
            />
          )}
        </AdminFormSection>
        <AdminFormSection title="账单与流量">
          <div className="admin-billing-grid">
            <div className="admin-billing-row admin-billing-row--cycle">
              <AdminDateField className="admin-billing-control admin-billing-control--expiry" name="expiry-date" label="到期日" defaultValue={node.expiryDate ?? ''} defaultPermanent={node.expiryPermanent} permanentLabel="设为永久" />
              <label className="admin-billing-control admin-billing-control--reset">
                <span>月流量重置日</span>
                <input name="monthly-reset-day" type="number" min="1" max="31" step="1" defaultValue={node.monthlyResetDay || 1} />
              </label>
              <AdminSegmentedField className="admin-billing-control admin-billing-control--cycle" name="billing-cycle" label="账单周期" defaultValue={normalizeBillingCycle(node.billingCycle)} options={billingCycleOptions} />
            </div>
            <div className="admin-billing-row admin-billing-row--traffic">
              <AdminSegmentedField className="admin-billing-control admin-billing-control--mode" name="billing-mode" label="流量计费口径" defaultValue={node.billingMode || 'both'} options={billingModeOptions} />
              <label className="admin-billing-control admin-billing-control--quota">
                <span>月配额</span>
                <input name="monthly-quota" type="number" min="0" step="0.01" defaultValue={formatQuotaValue(node.monthlyQuotaBytes)} />
              </label>
              <AdminSegmentedField className="admin-billing-control admin-billing-control--unit" name="monthly-quota-unit" label="配额单位" defaultValue={quotaUnitForBytes(node.monthlyQuotaBytes)} options={quotaUnitOptions} />
            </div>
          </div>
        </AdminFormSection>
        <AdminFormSection title="Agent 接入">
          <div className="admin-inline-actions admin-install-copy-row">
            <div className="admin-install-copy-menu">
              <button ref={installCopyButtonRef} className="admin-primary-action admin-install-copy-button" type="button" onClick={handleCopyInstallCommand} disabled={installCommandState.kind === 'loading'}>{installCommandState.kind === 'loading' ? '生成中…' : '复制安装命令'}</button>
              {installPlatformPickerOpen && <AdminInstallPlatformPopover state={installCommandState} style={installPlatformMenuStyle} onSelect={handleCopyInstallPlatform} />}
            </div>
            {installCopyState.kind !== 'idle' && <span className={`admin-inline-note${installCopyState.kind === 'ready' ? ' is-success' : installCopyState.kind === 'warning' ? ' is-warning' : ' is-error'}`}>{installCopyState.message}</span>}
          </div>
          {installCommandState.kind === 'error' && <div className="admin-install-error">安装命令生成失败：{installCommandState.message}</div>}
        </AdminFormSection>
        <div className="admin-modal-actions">
          {formError && <span className="admin-inline-note admin-modal-action-note is-error">{formError}</span>}
          <button type="submit" disabled={submitting}>{submitting ? '保存中…' : '保存服务器'}</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminTargetSection({ targets, nodes, onCreate, onUpdate, onDelete }: { targets: AdminProbeTarget[]; nodes: AdminNode[]; onCreate: (input: AdminProbeTargetInput) => MaybePromise; onUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => MaybePromise; onDelete: (targetId: string) => MaybePromise }) {
  const [creatingTarget, setCreatingTarget] = useState(false)
  const [editingTargetId, setEditingTargetId] = useState<string | null>(null)
  const editingTarget = editingTargetId ? targets.find((target) => target.id === editingTargetId) : undefined
  const sortedTargets = sortAdminProbeTargets(targets)

  return (
    <section className="admin-target-section admin-workspace-panel" aria-label="admin probe target list">
      <header className="admin-section-heading">
        <div>
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
          nodes={nodes}
          onClose={() => setCreatingTarget(false)}
          onCreate={onCreate}
        />
      )}

      {editingTarget && (
        <AdminTargetEditModal
          key={editingTarget.id}
          target={editingTarget}
          nodes={nodes}
          onClose={() => setEditingTargetId(null)}
          onUpdate={onUpdate}
        />
      )}
    </section>
  )
}

function AdminTargetList({ targets, onEdit, onDelete }: { targets: AdminProbeTarget[]; onEdit: (targetId: string) => void; onDelete: (targetId: string) => MaybePromise }) {
  const [pendingDelete, setPendingDelete] = useState<AdminProbeTarget | null>(null)

  return (
    <>
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
            <button className="admin-row-action is-icon is-danger" type="button" aria-label={`删除目标 ${target.name}`} title="删除目标" onClick={() => setPendingDelete(target)}><TrashActionIcon /><span className="sr-only">删除目标</span></button>
          </div>
        </article>
      ))}
    </div>
    {pendingDelete && (
      <AdminDeleteConfirmModal
        title="删除延迟监控"
        subjectName={pendingDelete.name}
        confirmLabel="删除延迟监控"
        onClose={() => setPendingDelete(null)}
        onConfirm={() => onDelete(pendingDelete.id)}
      />
    )}
    </>
  )
}

function AdminTargetCreateModal({ nodes, onCreate, onClose }: { nodes: AdminNode[]; onCreate: (input: AdminProbeTargetInput) => MaybePromise; onClose: () => void }) {
  const [targetType, setTargetType] = useState<ProbeType>('tcping')
  const [assignmentNodeIds, setAssignmentNodeIds] = useState<string[]>([])
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const name = String(formData.get('new-target-name') ?? '').trim()
    const type = normalizeTargetFormType(String(formData.get('new-target-type') ?? 'tcping'))
    const address = String(formData.get('new-target-address') ?? '').trim()
    const port = type === 'tcping' ? parsePositiveInt(String(formData.get('new-target-port') ?? '')) : null
    if (name === '' || address === '' || (type === 'tcping' && port === null)) return
    setSubmitting(true)
    setFormError(null)
    Promise.resolve(onCreate({
      name,
      type,
      address,
      port,
      count: parsePositiveInt(String(formData.get('new-target-count') ?? '')) ?? 3,
      timeoutMs: parsePositiveInt(String(formData.get('new-target-timeout-ms') ?? '')) ?? 1000,
      intervalSec: parsePositiveInt(String(formData.get('new-target-interval-sec') ?? '')) ?? 30,
      assignments: nodes.map((node) => ({ nodeId: node.id, enabled: assignmentNodeIds.includes(node.id) })),
    }))
      .then(() => onClose())
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '添加失败'))
      .finally(() => setSubmitting(false))
  }

  return (
    <AdminModal title="添加延迟监控目标" onClose={onClose}>
      <form className="admin-target-create-form admin-node-edit-form is-sectioned" aria-label="添加探针目标" onSubmit={handleSubmit}>
        <AdminFormSection title="目标信息">
          <div className="admin-form-grid">
            <label>
              <span>目标名称</span>
              <input name="new-target-name" autoComplete="off" placeholder="Example HTTPS" />
            </label>
            <AdminSegmentedField name="new-target-type" label="类型" value={targetType} onChange={(value) => setTargetType(normalizeTargetFormType(value))} options={targetTypeOptions} />
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
        {nodes.length > 0 && (
          <AdminFormSection title="启用服务器">
            <AdminExpandedCheckList
              title="已启用服务器"
              emptyText="暂无服务器"
              options={nodes.map((node) => ({ value: node.id, label: node.displayName || node.id }))}
              value={assignmentNodeIds}
              onChange={setAssignmentNodeIds}
            />
          </AdminFormSection>
        )}
        <AdminFormSection title="探测参数">
          <div className="admin-form-grid">
            <label>
              <span>次数</span>
              <input name="new-target-count" type="number" min="1" defaultValue="3" />
            </label>
            <label>
              <span>超时 ms</span>
              <input name="new-target-timeout-ms" type="number" min="1" defaultValue="1000" />
            </label>
            <label>
              <span>间隔 s</span>
              <input name="new-target-interval-sec" type="number" min="1" defaultValue="30" />
            </label>
          </div>
        </AdminFormSection>
        <div className="admin-modal-actions">
          {formError && <span className="admin-inline-note admin-modal-action-note is-error">{formError}</span>}
          <button type="submit" disabled={submitting}>{submitting ? '添加中…' : '添加目标'}</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminTargetEditModal({ target, nodes, onUpdate, onClose }: { target: AdminProbeTarget; nodes: AdminNode[]; onUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => MaybePromise; onClose: () => void }) {
  const [targetType, setTargetType] = useState<ProbeType>(target.type)
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const assignmentRows = targetAssignmentRows(target, nodes)
  const [assignmentNodeIds, setAssignmentNodeIds] = useState<string[]>(() => assignmentRows.filter((assignment) => assignment.enabled).map((assignment) => assignment.nodeId))
  const selectedAssignmentNodes = new Set(assignmentNodeIds)

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const type = normalizeTargetFormType(String(formData.get('target-type') ?? targetType))
    const port = type === 'tcping' ? parsePositiveInt(String(formData.get('target-port') ?? '')) : null
    if (type === 'tcping' && port === null) return
    setSubmitting(true)
    setFormError(null)
    Promise.resolve(onUpdate(target.id, {
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
            enabled: selectedAssignmentNodes.has(assignment.nodeId),
          }))
        : undefined,
    }))
      .then(() => onClose())
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '保存失败'))
      .finally(() => setSubmitting(false))
  }

  return (
    <AdminModal title={`编辑延迟监控 · ${target.name}`} onClose={onClose}>
      <form className="admin-target-edit-form admin-node-edit-form is-sectioned" aria-label={`${target.name} 探针目标编辑`} onSubmit={handleSubmit}>
        <AdminFormSection title="目标信息">
          <div className="admin-form-grid">
            <label>
              <span>目标名</span>
              <input name="target-name" defaultValue={target.name} autoComplete="off" />
            </label>
            <AdminSegmentedField name="target-type" label="类型" value={targetType} onChange={(value) => setTargetType(normalizeTargetFormType(value))} options={targetTypeOptions} />
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
          <AdminFormSection title="按服务器启用">
            <AdminExpandedCheckList
              title="已启用服务器"
              emptyText="暂无服务器"
              options={assignmentRows.map((assignment) => ({ value: assignment.nodeId, label: assignment.nodeDisplayName || assignment.nodeId }))}
              value={assignmentNodeIds}
              onChange={setAssignmentNodeIds}
            />
          </AdminFormSection>
        )}
        <div className="admin-modal-actions">
          {formError && <span className="admin-inline-note admin-modal-action-note is-error">{formError}</span>}
          <button type="submit" disabled={submitting}>{submitting ? '保存中…' : '保存目标'}</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminAlertRulesSection({ rules, nodes, onUpdate }: { rules: AdminAlertRule[]; nodes: AdminNode[]; onUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => MaybePromise }) {
  const [editingRule, setEditingRule] = useState<AdminAlertRule | null>(null)
  const [addingRule, setAddingRule] = useState(false)
  const addedRules = rules.filter((rule) => rule.enabled)
  const availableRules = rules.filter((rule) => !rule.enabled)

  return (
    <section className="admin-notification-block admin-alert-rule-section" aria-label="通知类型规则">
      <div className="admin-block-heading">
        <h4>通知类型</h4>
        <button className="admin-primary-action" type="button" onClick={() => setAddingRule(true)}>添加通知类型</button>
      </div>
      {addedRules.length === 0 && <div className="admin-state-card">还没有添加通知类型。</div>}
      {addedRules.length > 0 && <AdminAlertRuleList rules={addedRules} onEdit={setEditingRule} onUpdate={onUpdate} />}

      {addingRule && (
        <AdminAlertRuleAddModal
          rules={availableRules}
          nodes={nodes}
          onClose={() => setAddingRule(false)}
          onAdd={onUpdate}
        />
      )}

      {editingRule && (
        <AdminAlertRuleEditModal
          rule={editingRule}
          nodes={nodes}
          onClose={() => setEditingRule(null)}
          onUpdate={onUpdate}
        />
      )}
    </section>
  )
}

function AdminAlertRuleList({ rules, onEdit, onUpdate }: { rules: AdminAlertRule[]; onEdit: (rule: AdminAlertRule) => void; onUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => MaybePromise }) {
  const confirmDelete = (rule: AdminAlertRule) => {
    const ok = typeof window === 'undefined' ? true : window.confirm(`确认删除通知类型「${rule.name}」？`)
    if (ok) onUpdate(rule.id, { enabled: false })
  }

  return (
    <div className="admin-list admin-alert-rule-list" role="list" aria-label="通知类型列表">
      <div className="admin-list-head" aria-hidden="true">
        <span>通知类型</span>
        <span>状态</span>
        <span>操作</span>
      </div>
      {rules.map((rule) => (
        <article className="admin-list-row" role="listitem" key={rule.id}>
          <div className="admin-list-main">
            <strong>{rule.name}</strong>
            {formatAlertRuleNote(rule) && <small>{formatAlertRuleNote(rule)}</small>}
          </div>
          <AdminStatusBadge label={rule.enabled ? '启用中' : '已停用'} status={rule.enabled ? 'online' : 'disabled'} dataLabel="状态" />
          <div className="admin-row-actions admin-icon-actions">
            <button className="admin-row-action is-icon" type="button" aria-label={`编辑通知类型 ${rule.name}`} title="编辑通知类型" onClick={() => onEdit(rule)}><EditActionIcon /><span className="sr-only">编辑通知类型</span></button>
            <button className="admin-row-action is-icon is-danger" type="button" aria-label={`删除通知类型 ${rule.name}`} title="删除通知类型" onClick={() => confirmDelete(rule)}><TrashActionIcon /><span className="sr-only">删除通知类型</span></button>
          </div>
        </article>
      ))}
    </div>
  )
}

function AdminAlertRuleAddModal({ rules, nodes, onAdd, onClose }: { rules: AdminAlertRule[]; nodes: AdminNode[]; onAdd: (ruleId: string, input: AdminAlertRuleUpdateInput) => MaybePromise; onClose: () => void }) {
  const [submittingRuleId, setSubmittingRuleId] = useState<string | null>(null)
  const [formError, setFormError] = useState<string | null>(null)
  const addRule = (ruleId: string) => {
    setSubmittingRuleId(ruleId)
    setFormError(null)
    Promise.resolve(onAdd(ruleId, { enabled: true }))
      .then(() => onClose())
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '添加失败'))
      .finally(() => setSubmittingRuleId(null))
  }
  return (
    <AdminModal title="添加通知类型" onClose={onClose}>
      <div className="admin-alert-rule-add-form admin-node-edit-form is-sectioned" aria-label="添加通知类型">
        <AdminFormSection title="通知类型">
          <div className="admin-rule-picker" role="list" aria-label="可添加通知类型">
            {rules.length === 0 && <div className="admin-state-card">所有通知类型都已添加。</div>}
            {rules.map((rule) => (
              <article className="admin-rule-picker-row" role="listitem" key={rule.id}>
                <div className="admin-list-main">
                  <strong>{rule.name}</strong>
                  <small>{formatAlertRuleNote(rule) || formatAlertRuleScope(rule, nodes)}</small>
                </div>
                <button className="admin-primary-action" type="button" onClick={() => addRule(rule.id)} disabled={submittingRuleId !== null}>{submittingRuleId === rule.id ? '添加中…' : '添加'}</button>
              </article>
            ))}
          </div>
        </AdminFormSection>
        {formError && <div className="admin-install-error">{formError}</div>}
      </div>
    </AdminModal>
  )
}

function AdminAlertRuleEditModal({ rule, nodes, onUpdate, onClose }: { rule: AdminAlertRule; nodes: AdminNode[]; onUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => MaybePromise; onClose: () => void }) {
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const initialScopeNodeIds = rule.scopeNodeIds.length === 0 ? nodes.map((node) => node.id) : rule.scopeNodeIds
  const isRenewalRule = rule.metric === 'expiry_days'
  const isResourceRule = rule.category === 'resource' && rule.thresholdUnit === '%'
  const supportsDuration = !isRenewalRule
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const scopeNodeIds = nodes.filter((node) => formData.get(`rule-scope-${node.id}`) === 'on').map((node) => node.id)
    const renewalThreshold = isRenewalRule ? parseRenewalThreshold(String(formData.get('rule-renewal-days') ?? '')) : null
    const resourceThreshold = isResourceRule ? parsePercentage(String(formData.get('rule-threshold-percent') ?? '')) : null
    const durationSec = supportsDuration ? parseNonNegativeInt(String(formData.get('rule-duration-sec') ?? '')) : null
    setSubmitting(true)
    setFormError(null)
    Promise.resolve(onUpdate(rule.id, {
      enabled: formData.get('rule-enabled') === 'on',
      ...(isRenewalRule && renewalThreshold !== null ? { threshold: renewalThreshold } : {}),
      ...(isResourceRule && resourceThreshold !== null ? { threshold: resourceThreshold } : {}),
      ...(supportsDuration && durationSec !== null ? { durationSec } : {}),
      scopeNodeIds,
    }))
      .then(() => onClose())
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '保存失败'))
      .finally(() => setSubmitting(false))
  }

  return (
    <AdminModal title={`编辑通知类型 · ${rule.name}`} onClose={onClose}>
      <form className="admin-alert-rule-edit-form admin-node-edit-form is-sectioned" aria-label={`${rule.name} 通知类型编辑`} onSubmit={handleSubmit}>
        <AdminFormSection title="通知设置">
          <div className="admin-form-grid admin-alert-rule-settings-grid">
            {isRenewalRule && (
              <AdminSegmentedField
                name="rule-renewal-days"
                label="提前提醒"
                defaultValue={String(normalizeRenewalThreshold(rule.threshold))}
                options={renewalDayOptions.map((days) => ({ value: String(days), label: formatRenewalDayOption(days) }))}
              />
            )}
            {supportsDuration && (
              <label>
                <span>{isResourceRule ? '统计窗口 s' : '确认时间 s'}</span>
                <input name="rule-duration-sec" type="number" min="0" defaultValue={rule.durationSec} />
              </label>
            )}
            {isResourceRule && (
              <label>
                <span>平均超过 %</span>
                <input name="rule-threshold-percent" type="number" min="0" max="100" step="0.1" defaultValue={rule.threshold} />
              </label>
            )}
          </div>
        </AdminFormSection>
        <AdminFormSection title="通知状态">
          <label className="admin-node-toggle admin-alert-rule-enabled-toggle">
            <input name="rule-enabled" type="checkbox" defaultChecked={rule.enabled} />
            <span>启用通知类型</span>
          </label>
        </AdminFormSection>
        {nodes.length > 0 && (
          <AdminFormSection title="作用服务器">
            <div className="admin-rule-scope-list admin-target-assignment-list">
              {nodes.map((node) => (
                <label className="admin-node-toggle admin-target-assignment-toggle" key={node.id}>
                  <input name={`rule-scope-${node.id}`} type="checkbox" defaultChecked={initialScopeNodeIds.includes(node.id)} />
                  <span>{node.displayName || node.id}</span>
                </label>
              ))}
            </div>
          </AdminFormSection>
        )}
        <div className="admin-modal-actions">
          {formError && <span className="admin-inline-note admin-modal-action-note is-error">{formError}</span>}
          <button type="submit" disabled={submitting}>{submitting ? '保存中…' : '保存通知类型'}</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminNotificationsSection({ channels, rules, nodes, onChannelCreate, onChannelUpdate, onChannelDelete, onChannelTest, onRuleUpdate }: { channels: AdminNotificationChannel[]; rules: AdminAlertRule[]; nodes: AdminNode[]; onChannelCreate: (input: AdminNotificationChannelCreateInput) => MaybePromise; onChannelUpdate: (channelId: string, input: AdminNotificationChannelUpdateInput) => MaybePromise; onChannelDelete: (channelId: string) => MaybePromise; onChannelTest: (channelId: string) => void; onRuleUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => MaybePromise }) {
  const [creatingChannel, setCreatingChannel] = useState(false)
  const [editingChannel, setEditingChannel] = useState<AdminNotificationChannel | null>(null)

  return (
    <section className="admin-notification-section admin-workspace-panel" aria-label="admin notification settings">
      <header className="admin-section-heading">
        <div>
          <h3>通知</h3>
        </div>
        <button className="admin-primary-action" type="button" onClick={() => setCreatingChannel(true)}>添加通知渠道</button>
      </header>

      <section className="admin-notification-block" aria-label="通知渠道">
        <h4>通知渠道</h4>
        {channels.length === 0 && <div className="admin-state-card">还没有通知渠道。</div>}
        {channels.length > 0 && <AdminNotificationChannelList channels={channels} onDelete={onChannelDelete} onEdit={setEditingChannel} />}
      </section>

      <AdminAlertRulesSection rules={rules} nodes={nodes} onUpdate={onRuleUpdate} />

      {creatingChannel && (
        <AdminNotificationChannelCreateModal
          onClose={() => setCreatingChannel(false)}
          onCreate={onChannelCreate}
        />
      )}
      {editingChannel && (
        <AdminNotificationChannelEditModal
          channel={editingChannel}
          onClose={() => setEditingChannel(null)}
          onTest={onChannelTest}
          onUpdate={onChannelUpdate}
        />
      )}
    </section>
  )
}

function AdminNotificationChannelList({ channels, onDelete, onEdit }: { channels: AdminNotificationChannel[]; onDelete: (channelId: string) => void; onEdit: (channel: AdminNotificationChannel) => void }) {
  const confirmDelete = (channel: AdminNotificationChannel) => {
    const ok = typeof window === 'undefined' ? true : window.confirm(`确认删除通知渠道「${channel.name}」？`)
    if (ok) onDelete(channel.id)
  }

  return (
    <div className="admin-list admin-notification-list" role="list" aria-label="通知渠道列表">
      <div className="admin-list-head" aria-hidden="true">
        <span>渠道</span>
        <span>状态</span>
        <span>操作</span>
      </div>
      {channels.map((channel) => (
        <article className="admin-list-row" role="listitem" key={channel.id}>
          <div className="admin-list-main">
            <strong>{channel.name}</strong>
          </div>
          <AdminStatusBadge label={channel.enabled ? '启用中' : '已停用'} status={channel.enabled ? 'online' : 'disabled'} dataLabel="状态" />
          <div className="admin-row-actions admin-icon-actions">
            <button className="admin-row-action is-icon" type="button" aria-label={`编辑通知渠道 ${channel.name}`} title="编辑渠道" onClick={() => onEdit(channel)}><EditActionIcon /><span className="sr-only">编辑渠道</span></button>
            <button className="admin-row-action is-icon is-danger" type="button" aria-label={`删除通知渠道 ${channel.name}`} title="删除渠道" onClick={() => confirmDelete(channel)}><TrashActionIcon /><span className="sr-only">删除渠道</span></button>
          </div>
        </article>
      ))}
    </div>
  )
}

function AdminStatusBadge({ label, status, dataLabel }: { label: string; status: 'online' | 'disabled'; dataLabel?: string }) {
  return <span data-label={dataLabel} className={`admin-node-status admin-status-indicator status-${status}`}><i className="admin-status-dot" aria-hidden="true" />{label}</span>
}

function AdminNotificationChannelEditModal({ channel, onUpdate, onTest, onClose }: { channel: AdminNotificationChannel; onUpdate: (channelId: string, input: AdminNotificationChannelUpdateInput) => MaybePromise; onTest: (channelId: string) => void; onClose: () => void }) {
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const name = String(formData.get('channel-name') ?? '').trim()
    const destination = String(formData.get('channel-destination') ?? '').trim()
    const credential = String(formData.get('channel-credential') ?? '').trim()
    if (name === '') return
    setSubmitting(true)
    setFormError(null)
    Promise.resolve(onUpdate(channel.id, {
      name,
      ...(destination !== '' ? { destination } : {}),
      ...(credential !== '' ? { credential } : {}),
      enabled: formData.get('channel-enabled') === 'on',
    }))
      .then(() => onClose())
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '保存失败'))
      .finally(() => setSubmitting(false))
  }

  return (
    <AdminModal title="编辑通知渠道" onClose={onClose}>
      <form className="admin-notification-edit-form admin-node-edit-form is-sectioned" aria-label="编辑通知渠道" onSubmit={handleSubmit}>
        <AdminFormSection title="渠道配置">
          <div className="admin-form-grid admin-channel-form-grid">
            <label>
              <span>渠道名称</span>
              <input name="channel-name" autoComplete="off" defaultValue={channel.name} />
            </label>
            <label>
              <span>Telegram Chat ID</span>
              <input name="channel-destination" autoComplete="off" defaultValue={channel.destination} />
            </label>
            <AdminCredentialField
              name="channel-credential"
              placeholder={channel.credentialSet ? '留空则保留已保存 Token' : '请输入 Telegram Bot Token'}
            />
            <label className="admin-node-toggle admin-channel-enabled-toggle">
              <input name="channel-enabled" type="checkbox" defaultChecked={channel.enabled} />
              <span>启用渠道</span>
            </label>
          </div>
        </AdminFormSection>
        <div className="admin-modal-actions">
          {formError && <span className="admin-inline-note admin-modal-action-note is-error">{formError}</span>}
          <button type="button" onClick={() => onTest(channel.id)}>测试发送</button>
          <button type="submit" disabled={submitting}>{submitting ? '保存中…' : '保存通知渠道'}</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminNotificationChannelCreateModal({ onCreate, onClose }: { onCreate: (input: AdminNotificationChannelCreateInput) => MaybePromise; onClose: () => void }) {
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const name = String(formData.get('new-channel-name') ?? '').trim()
    const destination = String(formData.get('new-channel-destination') ?? '').trim()
    const credential = String(formData.get('new-channel-credential') ?? '').trim()
    if (name === '' || destination === '' || credential === '') return
    setSubmitting(true)
    setFormError(null)
    Promise.resolve(onCreate({
      name,
      destination,
      credential,
      enabled: formData.get('new-channel-enabled') === 'on',
    }))
      .then(() => onClose())
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '添加失败'))
      .finally(() => setSubmitting(false))
  }

  return (
    <AdminModal title="添加通知渠道" onClose={onClose}>
      <form className="admin-notification-create-form admin-node-edit-form is-sectioned" aria-label="添加通知渠道" onSubmit={handleSubmit}>
        <AdminFormSection title="渠道配置">
          <div className="admin-form-grid admin-channel-form-grid">
            <label>
              <span>渠道名称</span>
              <input name="new-channel-name" autoComplete="off" placeholder="Zeno Telegram" />
            </label>
            <label>
              <span>Telegram Chat ID</span>
              <input name="new-channel-destination" autoComplete="off" placeholder="请输入 Telegram Chat ID" />
            </label>
            <AdminCredentialField name="new-channel-credential" placeholder="请输入 Telegram Bot Token" />
            <label className="admin-node-toggle admin-channel-enabled-toggle">
              <input name="new-channel-enabled" type="checkbox" defaultChecked />
              <span>创建后启用渠道</span>
            </label>
          </div>
        </AdminFormSection>
        <div className="admin-modal-actions">
          {formError && <span className="admin-inline-note admin-modal-action-note is-error">{formError}</span>}
          <button type="submit" disabled={submitting}>{submitting ? '保存中…' : '保存通知渠道'}</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminModalLayer({ children }: { children: ReactNode }) {
  if (typeof document === 'undefined') return <>{children}</>
  return createPortal(children, document.body)
}

export function AdminCredentialField({ name, placeholder }: { name: string; placeholder: string }) {
  const [visible, setVisible] = useState(false)
  const inputId = useId()
  const actionLabel = visible ? '隐藏 Telegram Bot Token' : '显示 Telegram Bot Token'

  return (
    <div className="admin-form-control admin-secret-field">
      <label htmlFor={inputId}>Telegram Bot Token</label>
      <div className="admin-secret-input">
        <input
          id={inputId}
          name={name}
          type={visible ? 'text' : 'password'}
          autoComplete="new-password"
          placeholder={placeholder}
        />
        <button
          className="admin-secret-toggle"
          type="button"
          aria-label={actionLabel}
          aria-pressed={visible}
          aria-controls={inputId}
          title={actionLabel}
          onClick={() => setVisible((current) => !current)}
        >
          <SecretVisibilityIcon visible={visible} />
        </button>
      </div>
    </div>
  )
}

function AdminModal({ title, onClose, children, className, descriptionId, closeDisabled = false }: { title: string; onClose: () => void; children: ReactNode; className?: string; descriptionId?: string; closeDisabled?: boolean }) {
  return (
    <AdminModalLayer>
    <div className="admin-modal-backdrop" role="presentation">
      <section className={`admin-modal${className ? ` ${className}` : ''}`} role="dialog" aria-modal="true" aria-label={title} aria-describedby={descriptionId}>
        <header className="admin-modal-header">
          <div>
            <h3>{title}</h3>
          </div>
          <button className="admin-modal-close" type="button" onClick={onClose} aria-label="关闭弹窗" disabled={closeDisabled}>×</button>
        </header>
        <div className="admin-modal-body">{children}</div>
      </section>
    </div>
    </AdminModalLayer>
  )
}

export function AdminDeleteConfirmModal({ title, subjectName, confirmLabel, onConfirm, onClose }: { title: string; subjectName: string; confirmLabel: string; onConfirm: () => MaybePromise; onClose: () => void }) {
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const descriptionId = useId()

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    setSubmitting(true)
    setFormError(null)
    Promise.resolve(onConfirm())
      .then(onClose)
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '删除失败'))
      .finally(() => setSubmitting(false))
  }

  return (
    <AdminModal title={title} className="admin-delete-modal" descriptionId={descriptionId} closeDisabled={submitting} onClose={() => { if (!submitting) onClose() }}>
      <form className="admin-delete-confirm" aria-busy={submitting} onSubmit={handleSubmit}>
        <div id={descriptionId} className="admin-delete-confirm__content">
          <p className="admin-delete-confirm__lead">确认删除「<strong>{subjectName}</strong>」？</p>
          <p className="admin-delete-confirm__hint">删除后无法恢复。</p>
        </div>
        {(formError || submitting) && (
          <div className={`admin-delete-feedback${formError ? ' is-error' : ' is-pending'}`} aria-live="polite" aria-atomic="true">
            {formError ? `删除失败：${formError}` : '正在删除…'}
          </div>
        )}
        <div className="admin-modal-actions">
          <button type="button" disabled={submitting} onClick={onClose}>取消</button>
          <button className="is-danger" type="submit" disabled={submitting}>{submitting ? '删除中…' : confirmLabel}</button>
        </div>
      </form>
    </AdminModal>
  )
}

function AdminFormSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="admin-form-section" aria-label={title}>
      <h4 className="admin-form-section-title">{title}</h4>
      {children}
    </section>
  )
}

function AdminAppearancePresetCards({ value, onChange }: { value: AppearancePreset; onChange: (value: string) => void }) {
  return (
    <div className="admin-appearance-presets" role="radiogroup" aria-label="外观模板">
      <input type="hidden" name="appearance-preset" value={value} />
      {appearancePresetOptions.map((option) => {
        const preset = appearancePresets[option.value]
        const active = value === option.value
        return (
          <button key={option.value} type="button" role="radio" aria-checked={active} data-active={active} onClick={() => onChange(option.value)}>
            <span>{option.label}</span>
            <small>{Math.round(preset.cardOpacity * 100)}% · {preset.cardBlur}px</small>
          </button>
        )
      })}
    </div>
  )
}

function AdminAppearancePreview({ appearance }: { appearance: AppearanceValues }) {
  const previewStyle = {
    '--appearance-preview-color': appearance.themeColor,
    '--appearance-preview-bg': `rgba(var(--appearance-preview-surface-rgb), ${appearance.cardOpacity.toFixed(3)})`,
    '--appearance-preview-radius': `${Math.max(10, appearance.cardRadius - 4)}px`,
    '--appearance-preview-blur': `${appearance.cardBlur}px`,
    '--appearance-preview-shadow': `0 12px 28px -22px rgba(var(--appearance-preview-shadow-rgb), ${(0.08 + appearance.shadowStrength * 0.28).toFixed(3)})`,
  } as CSSProperties
  return (
    <div className="admin-appearance-preview" style={previewStyle} aria-hidden="true">
      <div className="admin-appearance-preview__card">
        <span />
        <strong>预览卡片</strong>
        <em>{Math.round(appearance.cardOpacity * 100)}% · {appearance.cardBlur}px · {Math.round(appearance.borderStrength * 100)}%</em>
      </div>
    </div>
  )
}

function AdminStyleRangeField({ name, label, value, min, max, step, onChange, formatValue }: { name: string; label: string; value: number; min: number; max: number; step: number; onChange: (value: number) => void; formatValue: (value: number) => string }) {
  return (
    <label className="admin-style-range">
      <span className="admin-style-range__head">
        <span>{label}</span>
        <strong>{formatValue(value)}</strong>
      </span>
      <input name={name} type="range" min={min} max={max} step={step} value={value} onChange={(event) => onChange(Number.parseFloat(event.currentTarget.value))} />
    </label>
  )
}

function parseSettingsNumber(formData: FormData, name: string, fallback: number): number {
  const parsed = Number.parseFloat(String(formData.get(name) ?? ''))
  return Number.isFinite(parsed) ? parsed : fallback
}

function AdminDateField({ name, label, defaultValue = '', defaultPermanent = false, disabled = false, permanentLabel, className = '' }: { name: string; label: string; defaultValue?: string | null; defaultPermanent?: boolean; disabled?: boolean; permanentLabel?: string; className?: string }) {
  const [value, setValue] = useState(defaultValue ?? '')
  const [permanent, setPermanent] = useState(defaultPermanent)
  const [month, setMonth] = useState(() => adminDateMonthStart(defaultValue))
  const [open, setOpen] = useState(false)
  const [openPanel, setOpenPanel] = useState<'year' | 'month' | null>(null)
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const popoverRef = useRef<HTMLDivElement | null>(null)
  const [popoverStyle, setPopoverStyle] = useState<CSSProperties>({})
  const selectedDate = parseAdminDateValue(value)
  const today = new Date()
  const visibleYear = month.getFullYear()
  const visibleMonth = month.getMonth()
  const yearOptions = adminDateYearOptions(visibleYear)
  const daysInMonth = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate()
  const leadingBlankDays = (new Date(month.getFullYear(), month.getMonth(), 1).getDay() + 6) % 7
  const calendarCells = [
    ...Array.from({ length: leadingBlankDays }, (_, index) => ({ key: `blank-${index}`, day: null })),
    ...Array.from({ length: daysInMonth }, (_, index) => ({ key: `day-${index + 1}`, day: index + 1 })),
  ]
  const pickDate = (date: Date) => {
    setValue(formatAdminDateValue(date))
    setPermanent(false)
    setMonth(new Date(date.getFullYear(), date.getMonth(), 1))
    setOpen(false)
    setOpenPanel(null)
  }
  const shiftMonth = (delta: number) => {
    setMonth((current) => new Date(current.getFullYear(), current.getMonth() + delta, 1))
    setOpenPanel(null)
  }
  const selectYear = (year: number) => {
    setMonth((current) => new Date(year, current.getMonth(), 1))
    setOpenPanel(null)
  }
  const selectMonth = (nextMonth: number) => {
    setMonth((current) => new Date(current.getFullYear(), nextMonth, 1))
    setOpenPanel(null)
  }
  const clearDate = () => {
    setValue('')
    setPermanent(false)
    setOpen(false)
    setOpenPanel(null)
  }
  const togglePermanent = () => {
    setValue('')
    setPermanent((current) => !current)
    setOpen(false)
    setOpenPanel(null)
  }

  useEffect(() => {
    setValue(defaultValue ?? '')
    setPermanent(defaultPermanent)
    setMonth(adminDateMonthStart(defaultValue))
    setOpen(false)
    setOpenPanel(null)
  }, [defaultValue, defaultPermanent])

  useLayoutEffect(() => {
    if (!open || disabled) return undefined
    const updatePopoverPosition = () => {
      const trigger = triggerRef.current
      if (!trigger) return
      const rect = trigger.getBoundingClientRect()
      const margin = 12
      const gap = 8
      const availableWidth = Math.max(296, window.innerWidth - margin * 2)
      const width = Math.min(340, availableWidth, Math.max(328, rect.width))
      const height = popoverRef.current?.offsetHeight ?? 354
      const left = Math.min(Math.max(margin, rect.left), Math.max(margin, window.innerWidth - width - margin))
      const belowTop = rect.bottom + gap
      const aboveTop = rect.top - height - gap
      const maxTop = Math.max(margin, window.innerHeight - height - margin)
      const preferredTop = belowTop + height <= window.innerHeight - margin || aboveTop < margin ? belowTop : aboveTop
      const top = Math.min(Math.max(margin, preferredTop), maxTop)
      setPopoverStyle({ position: 'fixed', top, left, width })
    }
    updatePopoverPosition()
    const frame = window.requestAnimationFrame(updatePopoverPosition)
    const settleTimer = window.setTimeout(updatePopoverPosition, 80)
    window.addEventListener('resize', updatePopoverPosition)
    window.addEventListener('scroll', updatePopoverPosition, true)
    return () => {
      window.cancelAnimationFrame(frame)
      window.clearTimeout(settleTimer)
      window.removeEventListener('resize', updatePopoverPosition)
      window.removeEventListener('scroll', updatePopoverPosition, true)
    }
  }, [open, disabled, visibleYear, visibleMonth, openPanel])

  const calendar = open && !disabled ? (
    <div ref={popoverRef} className="admin-date-popover" role="dialog" aria-label={`${label}日历`} style={popoverStyle}>
      <div className="admin-date-calendar-header">
        <button type="button" aria-label="上个月" onClick={() => shiftMonth(-1)}>‹</button>
        <div className="admin-date-current" aria-label="选择年月">
          <button className="admin-date-current-button" type="button" aria-expanded={openPanel === 'year'} onClick={() => setOpenPanel((current) => (current === 'year' ? null : 'year'))}>{visibleYear} 年</button>
          <button className="admin-date-current-button" type="button" aria-expanded={openPanel === 'month'} onClick={() => setOpenPanel((current) => (current === 'month' ? null : 'month'))}>{visibleMonth + 1} 月</button>
        </div>
        <button type="button" aria-label="下个月" onClick={() => shiftMonth(1)}>›</button>
      </div>
      {openPanel === 'year' && (
        <div className="admin-date-option-panel admin-date-year-panel" aria-label="年份选项">
          {yearOptions.map((year) => (
            <button className={year === visibleYear ? 'is-selected' : ''} type="button" key={year} onClick={() => selectYear(year)}>{year}</button>
          ))}
        </div>
      )}
      {openPanel === 'month' && (
        <div className="admin-date-option-panel admin-date-month-panel" aria-label="月份选项">
          {adminDateMonthOptions.map((option) => (
            <button className={option.value === visibleMonth ? 'is-selected' : ''} type="button" key={option.value} onClick={() => selectMonth(option.value)}>{option.label}</button>
          ))}
        </div>
      )}
      {openPanel === null && (
        <>
          <div className="admin-date-weekdays" aria-hidden="true">
            {adminDateWeekdays.map((weekday) => <span key={weekday}>{weekday}</span>)}
          </div>
          <div className="admin-date-grid">
            {calendarCells.map((cell) => {
              if (cell.day === null) return <span className="admin-date-empty" key={cell.key} />
              const date = new Date(month.getFullYear(), month.getMonth(), cell.day)
              const dateValue = formatAdminDateValue(date)
              const isSelected = selectedDate ? dateValue === formatAdminDateValue(selectedDate) : false
              const isToday = dateValue === formatAdminDateValue(today)
              return (
                <button className={`${isSelected ? 'is-selected' : ''}${isToday ? ' is-today' : ''}`} type="button" key={cell.key} onClick={() => pickDate(date)}>
                  {cell.day}
                </button>
              )
            })}
          </div>
          <div className="admin-date-actions">
            <button type="button" onClick={clearDate}>清空</button>
            <button type="button" onClick={() => pickDate(today)}>今天</button>
          </div>
        </>
      )}
    </div>
  ) : null

  return (
    <div className={['admin-form-control admin-date-field', className].filter(Boolean).join(' ')}>
      <span>{label}</span>
      <input type="hidden" name={name} value={value} disabled={disabled} />
      {permanentLabel && <input type="hidden" name={`${name.replace(/-date$/, '')}-permanent`} value={permanent ? '1' : '0'} disabled={disabled} />}
      <div className="admin-date-picker">
        <button ref={triggerRef} className="admin-date-trigger" type="button" aria-expanded={open} disabled={disabled} onClick={() => setOpen((current) => {
          if (current) setOpenPanel(null)
          return !current
        })}>
          <span className={value ? '' : 'is-placeholder'}>{value || 'YYYY-MM-DD'}</span>
          <CalendarIcon />
        </button>
        {permanentLabel && <button className={`admin-date-permanent${permanent ? ' is-active' : ''}`} type="button" aria-pressed={permanent} disabled={disabled} onClick={togglePermanent}>{permanentLabel}</button>}
        {calendar && (typeof document === 'undefined' ? calendar : createPortal(calendar, document.body))}
      </div>
    </div>
  )
}

const themeOptions = [
  { value: 'system', label: '跟随系统' },
  { value: 'light', label: '浅色' },
  { value: 'dark', label: '深色' },
]

const headerThemeOptions: Array<{ value: AdminTheme; label: string }> = [
  { value: 'system', label: '跟随系统' },
  { value: 'light', label: '浅色' },
  { value: 'dark', label: '深色' },
]

const billingModeOptions = [
  { value: 'both', label: '双向' },
  { value: 'in', label: '入站' },
  { value: 'out', label: '出站' },
  { value: 'max', label: '出入取大' },
]

const billingCycleOptions = [
  { value: '月', label: '月' },
  { value: '季', label: '季' },
  { value: '半年', label: '半年' },
  { value: '年', label: '年' },
  { value: '两年', label: '两年' },
  { value: '三年', label: '三年' },
  { value: '五年', label: '五年' },
]

const quotaUnitOptions = [
  { value: 'GB', label: 'GB' },
  { value: 'TB', label: 'TB' },
]

const adminDateWeekdays = ['一', '二', '三', '四', '五', '六', '日']

const adminDateMonthOptions = Array.from({ length: 12 }, (_, index) => ({ value: index, label: `${index + 1} 月` }))

function adminDateYearOptions(visibleYear: number): number[] {
  const currentYear = new Date().getFullYear()
  const start = Math.min(currentYear - 2, visibleYear - 4)
  const end = Math.max(currentYear + 10, visibleYear + 6)
  return Array.from({ length: end - start + 1 }, (_, index) => start + index)
}

function parseAdminDateValue(value?: string | null): Date | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec((value ?? '').trim())
  if (!match) return null
  const year = Number(match[1])
  const month = Number(match[2]) - 1
  const day = Number(match[3])
  const date = new Date(year, month, day)
  if (date.getFullYear() !== year || date.getMonth() !== month || date.getDate() !== day) return null
  return date
}

function formatAdminDateValue(date: Date): string {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function adminDateMonthStart(value?: string | null): Date {
  const parsed = parseAdminDateValue(value)
  const source = parsed ?? new Date()
  return new Date(source.getFullYear(), source.getMonth(), 1)
}

const targetTypeOptions = [
  { value: 'tcping', label: 'TCP Ping' },
  { value: 'ping', label: 'ICMP Ping' },
  { value: 'http_get', label: 'HTTP GET' },
]

function AdminSegmentedField({ name, label, options, value, defaultValue, disabled = false, onChange, className = '' }: { name: string; label: string; options: Array<{ value: string; label: string }>; value?: string; defaultValue?: string; disabled?: boolean; onChange?: (value: string) => void; className?: string }) {
  const [internalValue, setInternalValue] = useState(defaultValue ?? options[0]?.value ?? '')
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const popoverRef = useRef<HTMLDivElement | null>(null)
  const [popoverStyle, setPopoverStyle] = useState<CSSProperties>({})
  const selectedValue = value ?? internalValue
  const selectedOption = options.find((option) => option.value === selectedValue) ?? options[0]
  const setSelectedValue = (nextValue: string) => {
    if (disabled) return
    if (value === undefined) setInternalValue(nextValue)
    onChange?.(nextValue)
    setOpen(false)
  }

  useLayoutEffect(() => {
    if (!open || disabled) return undefined
    const updatePopoverPosition = () => {
      const trigger = triggerRef.current
      if (!trigger) return
      const rect = trigger.getBoundingClientRect()
      const margin = 12
      const gap = 8
      const width = Math.min(Math.max(rect.width, 160), Math.max(180, window.innerWidth - margin * 2))
      const height = popoverRef.current?.offsetHeight ?? Math.min(260, options.length * 40 + 12)
      const left = Math.min(Math.max(margin, rect.left), Math.max(margin, window.innerWidth - width - margin))
      const belowTop = rect.bottom + gap
      const aboveTop = rect.top - height - gap
      const preferredTop = belowTop + height <= window.innerHeight - margin || aboveTop < margin ? belowTop : aboveTop
      const top = Math.min(Math.max(margin, preferredTop), Math.max(margin, window.innerHeight - height - margin))
      setPopoverStyle({ position: 'fixed', top, left, width })
    }
    updatePopoverPosition()
    const frame = window.requestAnimationFrame(updatePopoverPosition)
    const settleTimer = window.setTimeout(updatePopoverPosition, 80)
    window.addEventListener('resize', updatePopoverPosition)
    window.addEventListener('scroll', updatePopoverPosition, true)
    return () => {
      window.cancelAnimationFrame(frame)
      window.clearTimeout(settleTimer)
      window.removeEventListener('resize', updatePopoverPosition)
      window.removeEventListener('scroll', updatePopoverPosition, true)
    }
  }, [open, disabled, options.length])

  useEffect(() => {
    if (!open) return undefined
    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target as Node | null
      if (target && (triggerRef.current?.contains(target) || popoverRef.current?.contains(target))) return
      setOpen(false)
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('pointerdown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [open])

  const classes = ['admin-form-control', 'admin-segmented-field admin-select-menu-field', className, disabled ? 'is-disabled' : ''].filter(Boolean).join(' ')
  const popover = open && !disabled ? (
    <div ref={popoverRef} className="admin-select-popover" role="listbox" aria-label={`${label}选项`} style={popoverStyle}>
      {options.map((option) => (
        <button key={option.value} type="button" role="option" aria-selected={selectedValue === option.value} data-active={selectedValue === option.value} onClick={() => setSelectedValue(option.value)}>
          <span>{option.label}</span>
        </button>
      ))}
    </div>
  ) : null
  return (
    <div className={classes}>
      <span>{label}</span>
      <input type="hidden" name={name} value={selectedValue} disabled={disabled} />
      <button ref={triggerRef} className="admin-select-trigger" type="button" aria-haspopup="listbox" aria-expanded={open} disabled={disabled} onClick={() => setOpen((current) => !current)}>
        <span>{selectedOption?.label ?? selectedValue}</span>
        <ChevronDownIcon expanded={open} />
      </button>
      {popover && (typeof document === 'undefined' ? popover : createPortal(popover, document.body))}
    </div>
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
      <div className="admin-expanded-checklist__header">
        <button className="admin-expanded-checklist__trigger" type="button" aria-expanded={expanded} onClick={() => setExpanded((current) => !current)}>
          <span>{title}</span>
          <small>{normalizedValue.length}/{options.length}</small>
          <ChevronDownIcon expanded={expanded} />
        </button>
        {options.length > 0 && <AdminBulkSelectButton selectedCount={normalizedValue.length} totalCount={options.length} onSelectAll={() => onChange(options.map((option) => option.value))} onClear={() => onChange([])} />}
      </div>
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

function AdminBulkSelectButton({ selectedCount, totalCount, onSelectAll, onClear }: { selectedCount: number; totalCount: number; onSelectAll: () => void; onClear: () => void }) {
  const allSelected = totalCount > 0 && selectedCount === totalCount
  return <button className="admin-bulk-select-button" type="button" onClick={allSelected ? onClear : onSelectAll}>{allSelected ? '清空' : '全选'}</button>
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

function normalizeTargetFormType(value: string): ProbeType {
  if (value === 'ping' || value === 'icmp') return 'ping'
  if (value === 'http_get' || value === 'http' || value === 'https') return 'http_get'
  return 'tcping'
}

function formatTargetEndpoint(target: AdminProbeTarget): string {
  return target.port ? `${target.address}:${target.port}` : target.address
}

function formatTargetAssignmentSummary(target: AdminProbeTarget): string {
  if (target.assignments.length === 0) return '未分配服务器'
  const enabled = target.assignments.filter((assignment) => assignment.enabled).length
  return `${enabled} / ${target.assignments.length} 服务器启用`
}

function formatAlertRuleScope(rule: AdminAlertRule, nodes: AdminNode[]): string {
  if (rule.scopeNodeIds.length === 0) return '全部服务器'
  const labels = rule.scopeNodeIds.map((nodeId) => {
    const node = nodes.find((candidate) => candidate.id === nodeId)
    return node?.displayName || nodeId
  })
  return labels.join('、')
}

function formatAlertRuleNote(rule: AdminAlertRule): string {
  if (rule.metric === 'expiry_days') return ''
  if (rule.category === 'resource' && rule.thresholdUnit === '%') {
    const windowLabel = rule.durationSec <= 0 ? '当前值' : `${formatDurationCompact(rule.durationSec)}平均`
    return `${windowLabel} ≥ ${formatPercentThreshold(rule.threshold)}%`
  }
  return rule.durationSec <= 0 ? '立即通知' : `${formatDurationCompact(rule.durationSec)}确认`
}

function formatPercentThreshold(value: number): string {
  if (Number.isInteger(value)) return String(value)
  return String(Math.round(value * 10) / 10)
}

function formatDurationCompact(seconds: number): string {
  const normalized = Math.max(0, Math.round(seconds))
  if (normalized === 0) return '立即'
  if (normalized % 3600 === 0) return `${normalized / 3600} 小时`
  if (normalized % 60 === 0) return `${normalized / 60} 分钟`
  return `${normalized} 秒`
}

function formatRenewalDayOption(days: number): string {
  if (days === 0) return '当天提醒'
  if (days === 15) return '提前半个月'
  if (days === 30) return '提前1个月'
  return `提前${days}天`
}

function parseRenewalThreshold(value: string): number | null {
  const parsed = parseNonNegativeInt(value)
  if (parsed === null || !renewalDayOptions.includes(parsed)) return null
  return parsed
}

function normalizeRenewalThreshold(value: number): number {
  const normalized = Math.max(0, Math.min(30, Math.round(value)))
  return renewalDayOptions.includes(normalized) ? normalized : 3
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

function parsePercentage(value: string): number | null {
  const trimmed = value.trim()
  if (trimmed === '') return null
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed) || parsed < 0 || parsed > 100) return null
  return parsed
}

async function copyTextToClipboard(text: string): Promise<void> {
  const copied = await copy(text)
  if (!copied) throw new Error('当前浏览器不支持自动复制，请手动选中复制。')
}

function parseMonthlyResetDay(value: string): number | null {
  const parsed = parseNonNegativeInt(value)
  if (!parsed || parsed < 1 || parsed > 31) return null
  return parsed
}

function normalizeBillingCycle(value?: string | null): string {
  const trimmed = (value ?? '').trim()
  if (trimmed.includes('五')) return '五年'
  if (trimmed.includes('三')) return '三年'
  if (trimmed.includes('两') || trimmed.includes('二') || trimmed.includes('2')) return '两年'
  if (trimmed.includes('半')) return '半年'
  if (trimmed.includes('季')) return '季'
  if (trimmed.includes('年')) return '年'
  return '月'
}

function quotaUnitForBytes(value: number | null): 'GB' | 'TB' {
  if (!value || value < 1024 ** 4) return 'GB'
  return 'TB'
}

function formatQuotaValue(value: number | null): string {
  if (!value || value <= 0) return ''
  const unit = quotaUnitForBytes(value)
  const divisor = unit === 'TB' ? 1024 ** 4 : 1024 ** 3
  return String(Math.round((value / divisor) * 100) / 100)
}

function parseQuota(value: string, unit: string): number | null {
  const trimmed = value.trim()
  if (trimmed === '') return null
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed) || parsed < 0) return null
  const multiplier = unit === 'TB' ? 1024 ** 4 : 1024 ** 3
  return Math.round(parsed * multiplier)
}

export function HomeOverviewPanel({ totalCount, onlineCount, offlineCount: _offlineCount, totalUp, totalDown, upSpeed, downSpeed }: HomeOverviewPanelProps) {
  const uploadRate = compactRateParts(upSpeed)
  const downloadRate = compactRateParts(downSpeed)
  return (
    <section className="home-summary" aria-label="server overview">
      <div className="home-summary__tile home-summary__status-line" aria-label="服务器在线摘要">
        <strong>{onlineCount} / {totalCount} 在线</strong>
      </div>

      <dl className="home-summary__tile home-summary__metric home-summary__metric--send" aria-label="traffic sent">
          <dt>发送</dt>
          <dd>{compactBytes(totalUp)}</dd>
      </dl>

      <dl className="home-summary__tile home-summary__metric home-summary__metric--receive" aria-label="traffic received">
          <dt>接收</dt>
          <dd>{compactBytes(totalDown)}</dd>
      </dl>

      <dl className="home-summary__tile home-summary__metric home-summary__metric--upload-rate home-summary__metric--rate" aria-label="upload speed">
          <dt>上传</dt>
          <dd><span className="home-summary__rate-value"><span>{uploadRate.value}</span><span className="home-summary__rate-gap" aria-hidden="true">&nbsp;</span><span className="home-summary__rate-unit">{uploadRate.unit}</span></span></dd>
      </dl>

      <dl className="home-summary__tile home-summary__metric home-summary__metric--download-rate home-summary__metric--rate" aria-label="download speed">
          <dt>下载</dt>
          <dd><span className="home-summary__rate-value"><span>{downloadRate.value}</span><span className="home-summary__rate-gap" aria-hidden="true">&nbsp;</span><span className="home-summary__rate-unit">{downloadRate.unit}</span></span></dd>
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

function SecretVisibilityIcon({ visible }: { visible: boolean }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M2.5 12s3.5-6 9.5-6 9.5 6 9.5 6-3.5 6-9.5 6-9.5-6-9.5-6Z" />
      <circle cx="12" cy="12" r="2.75" />
      {visible && <path d="m4 4 16 16" />}
    </svg>
  )
}

function CalendarIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M7 2.75a.75.75 0 0 1 .75.75V5h8.5V3.5a.75.75 0 0 1 1.5 0V5H19a3 3 0 0 1 3 3v10.25A3.75 3.75 0 0 1 18.25 22H5.75A3.75 3.75 0 0 1 2 18.25V8a3 3 0 0 1 3-3h1.25V3.5A.75.75 0 0 1 7 2.75ZM3.5 10v8.25a2.25 2.25 0 0 0 2.25 2.25h12.5a2.25 2.25 0 0 0 2.25-2.25V10h-17ZM5 6.5A1.5 1.5 0 0 0 3.5 8v.5h17V8A1.5 1.5 0 0 0 19 6.5H5Z" />
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

function MoonIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M20.99 12.58A8.5 8.5 0 1 1 11.42 3a6.6 6.6 0 0 0 9.57 9.57Z" />
    </svg>
  )
}

function MonitorIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <rect x="3" y="4" width="18" height="12" rx="2" />
      <path d="M8 20h8M12 16v4" />
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
