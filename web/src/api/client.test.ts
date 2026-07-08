import { afterEach, describe, expect, it, vi } from 'vitest'
import { createAdminNode, createAdminNotificationChannel, createAdminProbeTarget, deleteAdminNode, deleteAdminNotificationChannel, deleteAdminProbeTarget, fetchAdminAccount, fetchAdminAlertRules, fetchAdminNodes, fetchAdminNotificationChannels, fetchAdminProbeTargets, fetchAdminSettings, fetchPublicSettings, fetchServiceLatency, loginAdmin, logoutAdmin, normalizeAdminAlertRules, normalizeAdminNodes, normalizeAdminNotificationChannels, normalizeAdminProbeTargets, normalizeSettings, normalizeNodeLatency, normalizeNodeState, normalizeServiceLatency, normalizeSummary, requestAdminNodeInstallCommand, subscribeNodeLatency, subscribeNodeState, subscribeServiceLatency, subscribeSummary, testAdminNotificationChannel, updateAdminAccount, updateAdminAlertRule, updateAdminNode, updateAdminNotificationChannel, updateAdminNotificationType, updateAdminProbeTarget, updateAdminSettings } from './client'

describe('normalizeSummary', () => {
  it('maps controller snake_case JSON into frontend camelCase models', () => {
    const summary = normalizeSummary({
      nodes: [
        {
          id: 'hytron',
          display_name: 'Hytron',
          status: 'online',
          os: 'debian',
          os_version: '13',
          kernel: '6.12.0',
          virtualization: 'kvm',
          cpu_model: 'AMD EPYC',
          arch: 'aarch64',
          country_code: 'HK',
          subtitle: 'Hong Kong',
          cpu_cores: 2,
          expiry_label: '永 久',
          cpu_percent: 12.5,
          memory_used_bytes: 100,
          memory_total_bytes: 200,
          disk_used_bytes: 300,
          disk_total_bytes: 400,
          boot_time: '2026-07-02T01:00:00Z',
          load1: 0.42,
          load5: 0.35,
          load15: 0.28,
          uptime_seconds: 3600,
          net_in_speed_bps: 1024,
          net_out_speed_bps: 2048,
          net_in_total_bytes: 4096,
          net_out_total_bytes: 8192,
          billing_mode: 'max',
          monthly_reset_day: 15,
          monthly_period_start: '2026-06-15',
          monthly_period_end: '2026-07-14',
          monthly_billable_bytes: 1000,
          monthly_quota_bytes: 2000,
          latency_summary: {
            target_id: 'google',
            target_name: 'Google',
            median_ms: 1.2,
            avg_ms: 1.4,
            loss_percent: 0,
            updated_at: '2026-07-02T12:00:00Z',
          },
          latency_summaries: [
            {
              target_id: 'google',
              target_name: 'Google',
              median_ms: 1.2,
              avg_ms: 1.4,
              loss_percent: 0,
              updated_at: '2026-07-02T12:00:00Z',
            },
          ],
        },
      ],
      services: [
        { id: 'google', name: 'Google', type: 'http_get', address: 'https://www.google.com/generate_204', port: null, assigned_node_count: 10, reporting_node_count: 9, median_ms: 1.2, loss_percent: 0, updated_at: '2026-07-02T12:00:00Z' },
      ],
      latency_points: [
        { ts: '2026-07-02T12:00:00Z', target_id: 'google', target_name: 'Google', median_ms: null, loss_percent: 100 },
      ],
    })

    expect(summary.nodes[0].displayName).toBe('Hytron')
    expect(summary.nodes[0].arch).toBe('aarch64')
    expect(summary.nodes[0].countryCode).toBe('HK')
    expect(summary.nodes[0].cpuCores).toBe(2)
    expect(summary.nodes[0].expiryLabel).toBe('永 久')
    expect(summary.nodes[0].billingMode).toBe('max')
    expect(summary.nodes[0].monthlyResetDay).toBe(15)
    expect(summary.nodes[0].monthlyPeriodStart).toBe('2026-06-15')
    expect(summary.nodes[0].monthlyPeriodEnd).toBe('2026-07-14')
    expect(summary.nodes[0].monthlyBillableBytes).toBe(1000)
    expect(summary.nodes[0].latencySummary?.targetName).toBe('Google')
    expect(summary.nodes[0].latencySummaries?.[0].targetId).toBe('google')
    expect(summary.nodes[0].load1).toBe(0.42)
    expect(summary.nodes[0].load5).toBe(0.35)
    expect(summary.nodes[0].load15).toBe(0.28)
    expect(summary.nodes[0].uptimeSeconds).toBe(3600)
    expect(summary.nodes[0].osVersion).toBe('13')
    expect(summary.nodes[0].kernel).toBe('6.12.0')
    expect(summary.nodes[0].virtualization).toBe('kvm')
    expect(summary.nodes[0].cpuModel).toBe('AMD EPYC')
    expect(summary.nodes[0].bootTime).toBe('2026-07-02T01:00:00Z')
    expect(summary.latencyPoints[0].targetId).toBe('google')
    expect(summary.latencyPoints[0].medianMs).toBeNull()
    expect(summary.latencyPoints[0].lossPercent).toBe(100)
    expect(summary.services[0].name).toBe('Google')
    expect(summary.services[0].reportingNodeCount).toBe(9)
  })

  it('normalizes null collections from empty preview stores', () => {
    const summary = normalizeSummary({
      nodes: null,
      services: null,
      latency_points: null,
    })

    expect(summary.nodes).toEqual([])
    expect(summary.services).toEqual([])
    expect(summary.latencyPoints).toEqual([])
  })
})

describe('subscribeSummary', () => {
  const originalWebSocket = globalThis.WebSocket
  afterEach(() => {
    Object.defineProperty(globalThis, 'WebSocket', { configurable: true, writable: true, value: originalWebSocket })
    vi.restoreAllMocks()
  })

  it('prefers websocket summary events and closes the socket', () => {
    const instances: any[] = []
    class FakeWebSocket {
      url: string
      onmessage: ((event: MessageEvent<string>) => void) | null = null
      onerror: (() => void) | null = null
      onclose: (() => void) | null = null
      close = vi.fn(() => this.onclose?.())

      constructor(url: string) {
        this.url = url
        instances.push(this)
      }

      emitSummary(payload: unknown) {
        this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent<string>)
      }
    }
    Object.defineProperty(globalThis, 'WebSocket', { configurable: true, writable: true, value: FakeWebSocket })

    const onSummary = vi.fn()
    const onError = vi.fn()
    const unsubscribe = subscribeSummary(onSummary, onError)

    expect(new URL(instances[0].url).pathname).toBe('/api/public/v1/summary/ws')
    expect(new URL(instances[0].url).protocol).toBe('ws:')
    instances[0].emitSummary({
      nodes: [{ id: 'hytron', display_name: 'Hytron', status: 'online', os: 'debian', country_code: 'HK', cpu_percent: 1, memory_used_bytes: 2, memory_total_bytes: 4, disk_used_bytes: 8, disk_total_bytes: 16, net_in_speed_bps: 32, net_out_speed_bps: 64, net_in_total_bytes: 128, net_out_total_bytes: 256, monthly_billable_bytes: 384, monthly_quota_bytes: 512 }],
      services: [],
      latency_points: [],
    })

    expect(onSummary).toHaveBeenCalledWith(expect.objectContaining({
      nodes: [expect.objectContaining({ id: 'hytron', displayName: 'Hytron', netOutSpeedBps: 64 })],
    }))
    unsubscribe?.()
    expect(instances[0].close).toHaveBeenCalled()
    expect(onError).not.toHaveBeenCalled()
  })

  it('reconnects websocket closes without surfacing a fatal API error', () => {
    vi.useFakeTimers()
    const instances: any[] = []
    class FakeWebSocket {
      url: string
      onopen: (() => void) | null = null
      onmessage: ((event: MessageEvent<string>) => void) | null = null
      onerror: (() => void) | null = null
      onclose: (() => void) | null = null
      close = vi.fn(() => this.onclose?.())

      constructor(url: string) {
        this.url = url
        instances.push(this)
      }
    }
    Object.defineProperty(globalThis, 'WebSocket', { configurable: true, writable: true, value: FakeWebSocket })

    const onSummary = vi.fn()
    const onError = vi.fn()
    const unsubscribe = subscribeSummary(onSummary, onError)
    instances[0].onopen?.()
    instances[0].onclose?.()
    expect(onError).not.toHaveBeenCalled()

    vi.advanceTimersByTime(1250)
    expect(instances).toHaveLength(2)

    unsubscribe?.()
    vi.useRealTimers()
  })

})

describe('detail websocket subscriptions', () => {
  const originalWebSocket = globalThis.WebSocket

  afterEach(() => {
    Object.defineProperty(globalThis, 'WebSocket', { configurable: true, writable: true, value: originalWebSocket })
    vi.restoreAllMocks()
  })

  it('opens node state, node latency, and service latency websocket endpoints', () => {
    const instances: any[] = []
    class FakeWebSocket {
      url: string
      onmessage: ((event: MessageEvent<string>) => void) | null = null
      close = vi.fn()

      constructor(url: string) {
        this.url = url
        instances.push(this)
      }

      emit(payload: unknown) {
        this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent<string>)
      }
    }
    Object.defineProperty(globalThis, 'WebSocket', { configurable: true, writable: true, value: FakeWebSocket })

    const onNodeLatency = vi.fn()
    const onNodeState = vi.fn()
    const onServiceLatency = vi.fn()
    const stopNodeLatency = subscribeNodeLatency('hytron', '1d', onNodeLatency)
    const stopNodeState = subscribeNodeState('hytron', '1h', onNodeState)
    const stopServiceLatency = subscribeServiceLatency('google', '7d', onServiceLatency)

    expect(new URL(instances[0].url).pathname).toBe('/api/public/v1/nodes/hytron/latency/ws')
    expect(new URL(instances[0].url).searchParams.get('range')).toBe('1d')
    expect(new URL(instances[1].url).pathname).toBe('/api/public/v1/nodes/hytron/state/ws')
    expect(new URL(instances[1].url).searchParams.get('range')).toBe('1h')
    expect(new URL(instances[2].url).pathname).toBe('/api/public/v1/services/google/latency/ws')
    expect(new URL(instances[2].url).searchParams.get('range')).toBe('7d')

    instances[0].emit({ node_id: 'hytron', range: '1d', points: [{ ts: '2026-07-05T07:00:00Z', target_id: 'google', target_name: 'Google', median_ms: 1.2, loss_percent: 0 }] })
    instances[1].emit({ node_id: 'hytron', range: '1h', points: [{ ts: '2026-07-05T07:00:00Z', cpu_percent: 12, load1: 0.1, load5: 0.2, load15: 0.3, memory_used_bytes: 100, memory_total_bytes: 200, swap_used_bytes: 0, swap_total_bytes: 0, disk_used_bytes: 300, disk_total_bytes: 400, net_in_total_bytes: 500, net_out_total_bytes: 600, net_in_speed_bps: 700, net_out_speed_bps: 800, process_count: 90, tcp_connection_count: 10, udp_connection_count: 5, uptime_seconds: 60 }] })
    instances[2].emit({ target: { id: 'google', name: 'Google', type: 'http_get', address: 'https://www.google.com/generate_204', port: null, assigned_node_count: 1, reporting_node_count: 1, median_ms: 1.2, loss_percent: 0, updated_at: '2026-07-05T07:00:00Z' }, range: '7d', points: [{ ts: '2026-07-05T07:00:00Z', node_id: 'hytron', node_name: 'Hytron', median_ms: 1.3, loss_percent: 0 }] })

    expect(onNodeLatency).toHaveBeenCalledWith(expect.objectContaining({ nodeId: 'hytron', range: '1d' }))
    expect(onNodeState).toHaveBeenCalledWith(expect.objectContaining({ nodeId: 'hytron', range: '1h', points: [expect.objectContaining({ netOutSpeedBps: 800 })] }))
    expect(onServiceLatency).toHaveBeenCalledWith(expect.objectContaining({ target: expect.objectContaining({ id: 'google' }), range: '7d' }))

    stopNodeLatency?.()
    stopNodeState?.()
    stopServiceLatency?.()
    expect(instances.every((instance) => instance.close.mock.calls.length === 1)).toBe(true)
  })
})

describe('normalizeServiceLatency', () => {
  it('maps service latency points into chart-compatible node series', () => {
    const data = normalizeServiceLatency({
      target: { id: 'google', name: 'Google', type: 'http_get', address: 'https://www.google.com/generate_204', port: null, assigned_node_count: 10, reporting_node_count: 9, median_ms: 1.2, loss_percent: 0, updated_at: '2026-07-02T12:00:00Z' },
      range: '1d',
      points: [
        { ts: '2026-07-02T12:00:00Z', node_id: 'hytron', node_name: 'Hytron', median_ms: 1.4, loss_percent: 0 },
      ],
    })

    expect(data.target.id).toBe('google')
    expect(data.target.assignedNodeCount).toBe(10)
    expect(data.points[0].targetId).toBe('hytron')
    expect(data.points[0].targetName).toBe('Hytron')
  })

  it('expands compact Kulin-style node series into chart-compatible points', () => {
    const data = normalizeServiceLatency({
      target: { id: 'google', name: 'Google', type: 'http_get', address: 'https://www.google.com/generate_204', port: null, assigned_node_count: 10, reporting_node_count: 9, median_ms: 1.2, loss_percent: 0, updated_at: '2026-07-02T12:00:00Z' },
      range: '1d',
      series: [
        {
          node_id: 'hytron',
          node_name: 'Hytron',
          created_at: [Date.parse('2026-07-02T12:00:00Z')],
          median_ms: [1.3],
          avg_ms: [1.5],
          loss_percent: [0],
        },
      ],
    })

    expect(data.points).toEqual([
      expect.objectContaining({ ts: '2026-07-02T12:00:00.000Z', targetId: 'hytron', targetName: 'Hytron', medianMs: 1.3, avgMs: 1.5, lossPercent: 0 }),
    ])
  })
})

describe('fetchServiceLatency', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('fetches public service latency without admin credentials', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      target: { id: 'google', name: 'Google', type: 'http_get', address: 'https://www.google.com/generate_204', port: null, assigned_node_count: 10, reporting_node_count: 9, median_ms: 1.2, loss_percent: 0 },
      range: '7d',
      points: [],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const data = await fetchServiceLatency('google', '7d')

    expect(data.target.name).toBe('Google')
    expect(data.range).toBe('7d')
    expect(fetchMock).toHaveBeenCalledWith('/api/public/v1/services/google/latency?range=7d', {
      headers: { Accept: 'application/json' },
    })
  })
})

describe('normalizeNodeLatency', () => {
  it('keeps node id, range, and loss-only null latency points', () => {
    const data = normalizeNodeLatency({
      node_id: 'hytron',
      range: '1h',
      points: [
        { ts: '2026-07-02T12:00:00Z', target_id: 'telegram-dc1', target_name: 'Telegram DC1', median_ms: null, loss_percent: 100 },
        { ts: '2026-07-02T12:02:00Z', target_id: 'google', target_name: 'Google', median_ms: 0.8, loss_percent: 0 },
      ],
    })

    expect(data.nodeId).toBe('hytron')
    expect(data.range).toBe('1h')
    expect(data.points[0].targetName).toBe('Telegram DC1')
    expect(data.points[0].medianMs).toBeNull()
    expect(data.points[0].lossPercent).toBe(100)
  })

  it('expands compact Kulin-style target series into chart points', () => {
    const data = normalizeNodeLatency({
      node_id: 'hytron',
      range: '1d',
      series: [
        {
          target_id: 'google',
          target_name: 'Google',
          created_at: [Date.parse('2026-07-02T12:00:00Z'), Date.parse('2026-07-02T12:01:00Z')],
          median_ms: [1.2, null],
          avg_ms: [1.4, null],
          loss_percent: [0, 100],
        },
      ],
    })

    expect(data.points).toEqual([
      expect.objectContaining({ ts: '2026-07-02T12:00:00.000Z', targetId: 'google', targetName: 'Google', medianMs: 1.2, avgMs: 1.4, lossPercent: 0 }),
      expect.objectContaining({ ts: '2026-07-02T12:01:00.000Z', targetId: 'google', targetName: 'Google', medianMs: null, avgMs: null, lossPercent: 100 }),
    ])
  })
})

describe('normalizeNodeState', () => {
  it('maps persisted agent state history into frontend camelCase points', () => {
    const data = normalizeNodeState({
      node_id: 'hytron',
      range: '1h',
      points: [
        {
          ts: '2026-07-02T12:00:00Z',
          cpu_percent: 18.75,
          load1: 0.42,
          load5: 0.35,
          load15: 0.28,
          memory_used_bytes: 4096,
          memory_total_bytes: 8192,
          swap_used_bytes: 512,
          swap_total_bytes: 2048,
          disk_used_bytes: 1024,
          disk_total_bytes: 2048,
          net_in_total_bytes: 1000,
          net_out_total_bytes: 2000,
          net_in_speed_bps: 128,
          net_out_speed_bps: 256,
          process_count: 88,
          tcp_connection_count: 34,
          udp_connection_count: 12,
          uptime_seconds: 3601,
        },
      ],
    })

    expect(data.nodeId).toBe('hytron')
    expect(data.range).toBe('1h')
    expect(data.points[0].cpuPercent).toBe(18.75)
    expect(data.points[0].load1).toBe(0.42)
    expect(data.points[0].load5).toBe(0.35)
    expect(data.points[0].load15).toBe(0.28)
    expect(data.points[0].memoryUsedBytes).toBe(4096)
    expect(data.points[0].swapUsedBytes).toBe(512)
    expect(data.points[0].swapTotalBytes).toBe(2048)
    expect(data.points[0].netOutSpeedBps).toBe(256)
    expect(data.points[0].processCount).toBe(88)
    expect(data.points[0].tcpConnectionCount).toBe(34)
    expect(data.points[0].udpConnectionCount).toBe(12)
    expect(data.points[0].uptimeSeconds).toBe(3601)
  })

  it('normalizes old state payloads without extra metrics to nulls', () => {
    const data = normalizeNodeState({
      node_id: 'hytron',
      range: '1h',
      points: [
        {
          ts: '2026-07-02T12:00:00Z',
          cpu_percent: 18.75,
          memory_used_bytes: 4096,
          memory_total_bytes: 8192,
          disk_used_bytes: 1024,
          disk_total_bytes: 2048,
          net_in_total_bytes: 1000,
          net_out_total_bytes: 2000,
          net_in_speed_bps: 128,
          net_out_speed_bps: 256,
          uptime_seconds: 3601,
        },
      ],
    })

    expect(data.points[0].load1).toBeNull()
    expect(data.points[0].swapUsedBytes).toBeNull()
    expect(data.points[0].processCount).toBeNull()
    expect(data.points[0].tcpConnectionCount).toBeNull()
    expect(data.points[0].udpConnectionCount).toBeNull()
  })
})

describe('normalizeAdminNodes', () => {
  it('maps authenticated admin node inventory without requiring token fields', () => {
    const data = normalizeAdminNodes({
      nodes: [
        {
          id: 'hytron',
          display_name: 'Hytron',
          status: 'online',
          country_code: 'HK',
          region: 'Hong Kong',
          disabled: false,
          billing_mode: 'both',
          monthly_reset_day: 15,
          expiry_date: '2026-08-01',
          billing_cycle: '月付',
          display_order: 10,
          public_ipv4: '198.51.100.8',
          public_ipv6: '2001:db8::8',
          monthly_quota_bytes: 1099511627776,
          last_seen_at: '2026-07-03T00:00:00Z',
          created_at: '2026-07-02T00:00:00Z',
          updated_at: '2026-07-03T00:00:00Z',
          hostname: 'hytron-real',
          os_name: 'debian',
          os_version: '13',
          kernel: '6.12.0',
          arch: 'x86_64',
          virtualization: 'kvm',
          cpu_model: 'AMD EPYC',
          cpu_cores: 2,
          memory_total_bytes: 2147483648,
          disk_total_bytes: 42949672960,
          boot_time: '2026-07-02T01:00:00Z',
          agent_version: 'd206817',
        },
      ],
    })

    expect(data.nodes[0].id).toBe('hytron')
    expect(data.nodes[0].displayName).toBe('Hytron')
    expect(data.nodes[0].disabled).toBe(false)
    expect(data.nodes[0].agentVersion).toBe('d206817')
    expect(data.nodes[0].expiryDate).toBe('2026-08-01')
    expect(data.nodes[0].billingCycle).toBe('月付')
    expect(data.nodes[0].monthlyResetDay).toBe(15)
    expect(data.nodes[0].displayOrder).toBe(10)
    expect(data.nodes[0].publicIPv4).toBe('198.51.100.8')
    expect(data.nodes[0].publicIPv6).toBe('2001:db8::8')
    expect(data.nodes[0].monthlyQuotaBytes).toBe(1099511627776)
  })
})

describe('normalizeAdminProbeTargets', () => {
  it('maps authenticated probe target inventory and node assignments', () => {
    const data = normalizeAdminProbeTargets({
      targets: [
        {
          id: 'hytron-local',
          name: 'Hytron',
          type: 'tcping',
          address: '127.0.0.1',
          port: 18980,
          count: 3,
          timeout_ms: 1200,
          interval_sec: 60,
          display_order: 20,
          enabled: true,
          assignments: [
            { node_id: 'hytron', node_display_name: 'Hytron', enabled: true },
          ],
        },
      ],
    })

    expect(data.targets[0].id).toBe('hytron-local')
    expect(data.targets[0].timeoutMs).toBe(1200)
    expect(data.targets[0].intervalSec).toBe(60)
    expect(data.targets[0].displayOrder).toBe(20)
    expect(data.targets[0].assignments[0].nodeDisplayName).toBe('Hytron')
  })

  it('normalizes HTTP GET targets without a port', () => {
    const data = normalizeAdminProbeTargets({
      targets: [
        {
          id: 'zeno-health-http',
          name: 'Zeno Health HTTP',
          type: 'http_get',
          address: 'https://example.com/health',
          port: null,
          count: 2,
          timeout_ms: 1500,
          interval_sec: 60,
          display_order: 30,
          enabled: true,
          assignments: [],
        },
      ],
    })

    expect(data.targets[0].type).toBe('http_get')
    expect(data.targets[0].port).toBeNull()
    expect(data.targets[0].address).toBe('https://example.com/health')
  })

  it('normalizes null assignment lists to an empty array', () => {
    const data = normalizeAdminProbeTargets({
      targets: [
        {
          id: 'orphan-target',
          name: 'Orphan Target',
          type: 'tcping',
          address: 'example.com',
          port: 443,
          count: 3,
          timeout_ms: 1200,
          interval_sec: 60,
          display_order: 40,
          enabled: true,
          assignments: null as never,
        },
      ],
    })

    expect(data.targets[0].assignments).toEqual([])
  })
})

describe('normalizeAdminNotifications', () => {
  it('maps channels with editable credentials for admin forms', () => {
    const channels = normalizeAdminNotificationChannels({
      channels: [
        {
          id: 'zeno-telegram',
          name: 'Zeno Telegram',
          destination: '7579942307',
          credential: 'telegram-bot-token',
          credential_set: true,
          enabled: false,
          created_at: '2026-07-03T00:00:00Z',
          updated_at: '2026-07-03T00:00:00Z',
        },
      ],
    })

    expect(channels.channels[0].credentialSet).toBe(true)
    expect(channels.channels[0].credential).toBe('telegram-bot-token')
  })
})

describe('normalizeSettings', () => {
  it('maps public/admin settings into frontend camelCase models', () => {
    const settings = normalizeSettings({
      site_title: '水饺监控',
      site_subtitle: 'VPS 状态总览',
      logo_url: '/assets/logo/custom.png',
      theme: 'dark',
      agent_controller_url: 'https://zeno.example.com',
      background_url: 'https://example.com/bg.webp',
      updated_at: '2026-07-04T12:00:00Z',
    })

    expect(settings.siteTitle).toBe('水饺监控')
    expect(settings.siteSubtitle).toBe('VPS 状态总览')
    expect(settings.logoUrl).toBe('/assets/logo/custom.png')
    expect(settings.theme).toBe('dark')
    expect(settings.agentControllerUrl).toBe('https://zeno.example.com')
    expect(settings.backgroundUrl).toBe('https://example.com/bg.webp')
    expect(settings.updatedAt).toBe('2026-07-04T12:00:00Z')
  })
})

describe('fetchAdminNodes', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('sends the admin token in X-Admin-Token and never in the URL', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ nodes: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await fetchAdminNodes('admin-pass')

    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/nodes', {
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
  })
})

describe('fetchAdminProbeTargets', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('sends the admin token in X-Admin-Token and never in the URL', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ targets: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await fetchAdminProbeTargets('admin-pass')

    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/probe-targets', {
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
  })
})

describe('admin auth client', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('logs in, logs out, and updates the admin account', async () => {
    const fetchMock = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      const textUrl = String(url)
      if (textUrl.endsWith('/login')) return new Response(JSON.stringify({ username: 'admin', token: 'session-token' }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (textUrl.endsWith('/account') && !init?.method) return new Response(JSON.stringify({ account: { username: 'admin' } }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (textUrl.endsWith('/account')) return new Response(JSON.stringify({ username: 'zeno-admin', token: 'account-session-token' }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      return new Response(null, { status: 204 })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await expect(loginAdmin('admin', 'admin-pass')).resolves.toEqual({ username: 'admin', token: 'session-token' })
    await expect(fetchAdminAccount('session-token')).resolves.toEqual({ username: 'admin' })
    await expect(updateAdminAccount('session-token', 'zeno-admin', 'admin-pass', 'new-admin-pass')).resolves.toEqual({ username: 'zeno-admin', token: 'account-session-token' })
    await expect(logoutAdmin('account-session-token')).resolves.toBeUndefined()

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/admin/v1/login', {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: 'admin', password: 'admin-pass' }),
    })
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/admin/v1/account', {
      headers: { Accept: 'application/json', 'X-Admin-Token': 'session-token' },
    })
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/admin/v1/account', {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-Admin-Token': 'session-token' },
      body: JSON.stringify({ username: 'zeno-admin', current_password: 'admin-pass', new_password: 'new-admin-pass' }),
    })
    expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/admin/v1/logout', {
      method: 'POST',
      headers: { 'X-Admin-Token': 'account-session-token' },
    })
  })
})

describe('fetchSettings', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('fetches public settings without admin credentials', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      site_title: '水饺监控',
      site_subtitle: 'VPS 状态总览',
      logo_url: '/assets/logo/custom.png',
      theme: 'dark',
      agent_controller_url: 'https://zeno.example.com',
      background_url: 'https://example.com/desktop-bg.webp',
      desktop_background_url: 'https://example.com/desktop-bg.webp',
      mobile_background_url: 'https://example.com/mobile-bg.webp',
      updated_at: '2026-07-04T12:00:00Z',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const settings = await fetchPublicSettings()

    expect(settings.siteTitle).toBe('水饺监控')
    expect(settings.logoUrl).toBe('/assets/logo/custom.png')
    expect(settings).not.toHaveProperty('avatarUrl')
    expect(settings.desktopBackgroundUrl).toBe('https://example.com/desktop-bg.webp')
    expect(settings.mobileBackgroundUrl).toBe('https://example.com/mobile-bg.webp')
    expect(fetchMock).toHaveBeenCalledWith('/api/public/v1/settings', {
      headers: { Accept: 'application/json' },
    })
  })

  it('fetches and updates admin settings with X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async (url: string | URL | Request) => new Response(JSON.stringify({
      settings: {
        site_title: String(url).includes('admin') ? '水饺监控' : 'Zeno',
        site_subtitle: 'VPS 状态总览',
        logo_url: '/assets/logo/custom.png',
        theme: 'dark',
        agent_controller_url: 'https://zeno.example.com',
        background_url: 'https://example.com/desktop-bg.webp',
        desktop_background_url: 'https://example.com/desktop-bg.webp',
        mobile_background_url: 'https://example.com/mobile-bg.webp',
        updated_at: '2026-07-04T12:00:00Z',
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await fetchAdminSettings('admin-pass')
    const settings = await updateAdminSettings('admin-pass', {
      siteTitle: '水饺监控',
      siteSubtitle: 'VPS 状态总览',
      logoUrl: '/assets/logo/custom.png',
      theme: 'dark',
      agentControllerUrl: 'https://zeno.example.com',
      backgroundUrl: 'https://example.com/desktop-bg.webp',
      desktopBackgroundUrl: 'https://example.com/desktop-bg.webp',
      mobileBackgroundUrl: 'https://example.com/mobile-bg.webp',
    })

    expect(settings.backgroundUrl).toBe('https://example.com/desktop-bg.webp')
    expect(settings.logoUrl).toBe('/assets/logo/custom.png')
    expect(settings.agentControllerUrl).toBe('https://zeno.example.com')
    expect(settings).not.toHaveProperty('avatarUrl')
    expect(settings.desktopBackgroundUrl).toBe('https://example.com/desktop-bg.webp')
    expect(settings.mobileBackgroundUrl).toBe('https://example.com/mobile-bg.webp')
    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/admin/v1/settings', {
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/admin/v1/settings', {
      method: 'PATCH',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({
        site_title: '水饺监控',
        site_subtitle: 'VPS 状态总览',
        logo_url: '/assets/logo/custom.png',
        theme: 'dark',
        agent_controller_url: 'https://zeno.example.com',
        background_url: 'https://example.com/desktop-bg.webp',
        desktop_background_url: 'https://example.com/desktop-bg.webp',
        mobile_background_url: 'https://example.com/mobile-bg.webp',
      }),
    })
  })
})

describe('fetchAdminNotificationChannels', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('fetches notification channels with X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async (url: string | URL | Request) => {
      if (String(url).includes('notification-channels')) {
        return new Response(JSON.stringify({ channels: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      return new Response(JSON.stringify({ types: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await fetchAdminNotificationChannels('admin-pass')

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/admin/v1/notification-channels', {
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
  })
})

describe('updateAdminNode', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('patches editable node fields with the admin token in X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      node: {
        id: 'hytron',
        display_name: 'Hytron Edited',
        status: 'disabled',
        country_code: 'HK',
        region: 'Hong Kong',
        home_probe_target_id: 'cloudflare',
        disabled: true,
        billing_mode: 'max',
        monthly_reset_day: 20,
        expiry_date: '2026-08-01',
        billing_cycle: '月付',
        display_order: 10,
        public_ipv4: '198.51.100.8',
        public_ipv6: '2001:db8::8',
        monthly_quota_bytes: 123456789,
        created_at: '2026-07-02T00:00:00Z',
        updated_at: '2026-07-03T00:00:00Z',
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const node = await updateAdminNode('admin-pass', 'hytron', {
      displayName: 'Hytron Edited',
      countryCode: 'HK',
      region: 'Hong Kong',
      homeProbeTargetId: 'cloudflare',
      expiryDate: '2026-08-01',
      billingCycle: '月付',
      billingMode: 'max',
      monthlyResetDay: 20,
      displayOrder: 10,
      publicIPv4: '198.51.100.8',
      publicIPv6: '2001:db8::8',
      monthlyQuotaBytes: 123456789,
      disabled: true,
    })

    expect(node.displayName).toBe('Hytron Edited')
    expect(node.disabled).toBe(true)
    expect(node.monthlyQuotaBytes).toBe(123456789)
    expect(node.homeProbeTargetId).toBe('cloudflare')
    expect(node.expiryDate).toBe('2026-08-01')
    expect(node.billingCycle).toBe('月付')
    expect(node.billingMode).toBe('max')
    expect(node.monthlyResetDay).toBe(20)
    expect(node.displayOrder).toBe(10)
    expect(node.publicIPv4).toBe('198.51.100.8')
    expect(node.publicIPv6).toBe('2001:db8::8')
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/nodes/hytron', {
      method: 'PATCH',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({
        display_name: 'Hytron Edited',
        country_code: 'HK',
        region: 'Hong Kong',
        home_probe_target_id: 'cloudflare',
        expiry_date: '2026-08-01',
        billing_cycle: '月付',
        billing_mode: 'max',
        monthly_reset_day: 20,
        display_order: 10,
        public_ipv4: '198.51.100.8',
        public_ipv6: '2001:db8::8',
        monthly_quota_bytes: 123456789,
        disabled: true,
      }),
    })
  })
})

describe('deleteAdminNode', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('deletes a node with the admin token in X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await deleteAdminNode('admin-pass', 'hytron')

    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/nodes/hytron', {
      method: 'DELETE',
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
  })
})

describe('createAdminNode', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('creates a backend-first node with editable fields and the admin token in X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      node: {
        id: 'new-server-a1b2c3d4',
        display_name: 'New Server',
        status: 'no_data',
        country_code: 'US',
        region: 'Los Angeles',
        disabled: false,
        billing_mode: 'out',
        monthly_reset_day: 10,
        expiry_date: '2026-09-01',
        billing_cycle: '月付',
        display_order: 20,
        public_ipv4: '203.0.113.20',
        public_ipv6: '2001:db8::20',
        monthly_quota_bytes: 1099511627776,
        created_at: '2026-07-03T00:00:00Z',
        updated_at: '2026-07-03T00:00:00Z',
      },
    }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const node = await createAdminNode('admin-pass', {
      displayName: 'New Server',
      countryCode: 'US',
      region: 'Los Angeles',
      expiryDate: '2026-09-01',
      billingCycle: '月付',
      billingMode: 'out',
      monthlyResetDay: 10,
      displayOrder: 20,
      publicIPv4: '203.0.113.20',
      publicIPv6: '2001:db8::20',
      monthlyQuotaBytes: 1099511627776,
    })

    expect(node.id).toBe('new-server-a1b2c3d4')
    expect(node.status).toBe('no_data')
    expect(node.displayOrder).toBe(20)
    expect(node.publicIPv4).toBe('203.0.113.20')
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/nodes', {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({
        display_name: 'New Server',
        country_code: 'US',
        region: 'Los Angeles',
        expiry_date: '2026-09-01',
        billing_cycle: '月付',
        billing_mode: 'out',
        monthly_reset_day: 10,
        display_order: 20,
        public_ipv4: '203.0.113.20',
        public_ipv6: '2001:db8::20',
        monthly_quota_bytes: 1099511627776,
      }),
    })
  })
})

describe('createAdminProbeTarget', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('creates a probe target with the admin token in X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      target: {
        id: 'example-https-a1b2c3d4',
        name: 'Example HTTPS',
        type: 'tcping',
        address: 'example.com',
        port: 443,
        count: 5,
        timeout_ms: 1500,
        interval_sec: 90,
        display_order: 20,
        enabled: true,
        assignments: [{ node_id: 'hytron', node_display_name: 'Hytron', enabled: true }],
      },
    }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const target = await createAdminProbeTarget('admin-pass', {
      name: 'Example HTTPS',
      type: 'tcping',
      address: 'example.com',
      port: 443,
      count: 5,
      timeoutMs: 1500,
      intervalSec: 90,
      displayOrder: 20,
    })

    expect(target.id).toBe('example-https-a1b2c3d4')
    expect(target.assignments[0].nodeId).toBe('hytron')
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/probe-targets', {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({
        name: 'Example HTTPS',
        type: 'tcping',
        address: 'example.com',
        port: 443,
        count: 5,
        timeout_ms: 1500,
        interval_sec: 90,
        display_order: 20,
      }),
    })
  })

  it('creates an HTTP GET probe target without a separate port', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      target: {
        id: 'zeno-health-http',
        name: 'Zeno Health HTTP',
        type: 'http_get',
        address: 'https://example.com/health',
        port: null,
        count: 2,
        timeout_ms: 1500,
        interval_sec: 60,
        display_order: 30,
        enabled: true,
        assignments: [],
      },
    }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const target = await createAdminProbeTarget('admin-pass', {
      name: 'Zeno Health HTTP',
      type: 'http_get',
      address: 'https://example.com/health',
      port: null,
      count: 2,
      timeoutMs: 1500,
      intervalSec: 60,
      displayOrder: 30,
    })

    expect(target.type).toBe('http_get')
    expect(target.port).toBeNull()
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/probe-targets', {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({
        name: 'Zeno Health HTTP',
        type: 'http_get',
        address: 'https://example.com/health',
        port: null,
        count: 2,
        timeout_ms: 1500,
        interval_sec: 60,
        display_order: 30,
      }),
    })
  })
})

describe('updateAdminProbeTarget', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('patches editable probe target fields with the admin token in X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      target: {
        id: 'hytron-local',
        name: 'Local Controller',
        type: 'tcping',
        address: '127.0.0.1',
        port: 18981,
        count: 4,
        timeout_ms: 900,
        interval_sec: 30,
        display_order: 40,
        enabled: false,
        assignments: [{ node_id: 'hytron', node_display_name: 'Hytron', enabled: true }],
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const target = await updateAdminProbeTarget('admin-pass', 'hytron-local', {
      name: 'Local Controller',
      address: '127.0.0.1',
      port: 18981,
      count: 4,
      timeoutMs: 900,
      intervalSec: 30,
      displayOrder: 40,
      enabled: false,
      assignments: [
        { nodeId: 'hytron', enabled: false },
        { nodeId: 'backup', enabled: true },
      ],
    })

    expect(target.name).toBe('Local Controller')
    expect(target.enabled).toBe(false)
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/probe-targets/hytron-local', {
      method: 'PATCH',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({
        name: 'Local Controller',
        address: '127.0.0.1',
        port: 18981,
        count: 4,
        timeout_ms: 900,
        interval_sec: 30,
        display_order: 40,
        enabled: false,
        assignments: [
          { node_id: 'hytron', enabled: false },
          { node_id: 'backup', enabled: true },
        ],
      }),
    })
  })

  it('patches HTTP GET probe targets with a null port to clear TCP state', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      target: {
        id: 'hytron-local',
        name: 'Zeno Health HTTP',
        type: 'http_get',
        address: 'https://example.com/health',
        port: null,
        count: 2,
        timeout_ms: 1500,
        interval_sec: 60,
        display_order: 50,
        enabled: true,
        assignments: [{ node_id: 'hytron', node_display_name: 'Hytron', enabled: true }],
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const target = await updateAdminProbeTarget('admin-pass', 'hytron-local', {
      name: 'Zeno Health HTTP',
      type: 'http_get',
      address: 'https://example.com/health',
      port: null,
      count: 2,
      timeoutMs: 1500,
      intervalSec: 60,
      displayOrder: 50,
      enabled: true,
    })

    expect(target.type).toBe('http_get')
    expect(target.port).toBeNull()
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/probe-targets/hytron-local', {
      method: 'PATCH',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({
        name: 'Zeno Health HTTP',
        type: 'http_get',
        address: 'https://example.com/health',
        port: null,
        count: 2,
        timeout_ms: 1500,
        interval_sec: 60,
        display_order: 50,
        enabled: true,
      }),
    })
  })
})

describe('deleteAdminProbeTarget', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('deletes a probe target with the admin token in X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await deleteAdminProbeTarget('admin-pass', 'hytron-local')

    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/probe-targets/hytron-local', {
      method: 'DELETE',
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
  })
})

describe('admin alert rules', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('normalizes notification types and fetches them with X-Admin-Token only', async () => {
    const apiPayload = {
      rules: [
        {
          id: 'cpu_high',
          name: 'CPU 使用率',
          category: 'resource',
          metric: 'cpu_percent',
          comparator: '>=',
          threshold: 90,
          threshold_unit: '%',
          duration_sec: 300,
          enabled: true,
          notification_event_type: 'probe_unhealthy',
          notification_label: '异常',
          description: '',
          scope_node_ids: ['hytron'],
          created_at: '2026-07-03T00:00:00Z',
          updated_at: '2026-07-03T00:00:00Z',
        },
      ],
    }
    const normalized = normalizeAdminAlertRules(apiPayload)
    expect(normalized.rules[0].thresholdUnit).toBe('%')
    expect(normalized.rules[0].durationSec).toBe(300)
    expect(normalized.rules[0].notificationLabel).toBe('异常')
    expect(normalized.rules[0].scopeNodeIds).toEqual(['hytron'])

    const fetchMock = vi.fn(async () => new Response(JSON.stringify(apiPayload), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const fetched = await fetchAdminAlertRules('admin-pass')
    expect(fetched.rules[0].metric).toBe('cpu_percent')
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/alert-rules', {
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
  })

  it('updates notification type enablement threshold and duration without putting admin token in the URL', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({
      rule: {
        id: 'cpu_high',
        name: 'CPU 使用率',
        category: 'resource',
        metric: 'cpu_percent',
        comparator: '>=',
        threshold: 95.5,
        threshold_unit: '%',
        duration_sec: 600,
        enabled: false,
        notification_event_type: 'probe_unhealthy',
        notification_label: '异常',
        description: '',
        scope_node_ids: ['hytron', 'backup'],
        created_at: '2026-07-03T00:00:00Z',
        updated_at: '2026-07-03T00:10:00Z',
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const rule = await updateAdminAlertRule('admin-pass', 'cpu_high', { enabled: false, threshold: 95.5, durationSec: 600, scopeNodeIds: ['hytron', 'backup'] })

    expect(rule.enabled).toBe(false)
    expect(rule.threshold).toBe(95.5)
    expect(rule.durationSec).toBe(600)
    expect(rule.scopeNodeIds).toEqual(['hytron', 'backup'])
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/alert-rules/cpu_high', {
      method: 'PATCH',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({ enabled: false, threshold: 95.5, duration_sec: 600, scope_node_ids: ['hytron', 'backup'] }),
    })
    const calls = fetchMock.mock.calls as unknown as Array<[RequestInfo | URL, RequestInit?]>
    expect(String(calls[0]?.[0])).not.toContain('admin-pass')
  })

})


describe('notification writes', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('creates and toggles notification config without placing credentials in URLs', async () => {
    const fetchMock = vi.fn(async (url: string | URL | Request) => {
      if (String(url).endsWith('/notification-channels')) {
        return new Response(JSON.stringify({
          channel: {
            id: 'zeno-telegram',
            name: 'Zeno Telegram',
              destination: '7579942307',
            credential_set: true,
            enabled: true,
            created_at: '2026-07-03T00:00:00Z',
            updated_at: '2026-07-03T00:00:00Z',
          },
        }), { status: 201, headers: { 'Content-Type': 'application/json' } })
      }
      if (String(url).includes('/notification-channels/')) {
        return new Response(JSON.stringify({
          channel: {
            id: 'zeno-telegram',
            name: 'Zeno Telegram',
              destination: '7579942307',
            credential_set: true,
            enabled: false,
            created_at: '2026-07-03T00:00:00Z',
            updated_at: '2026-07-03T00:00:00Z',
          },
        }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      return new Response(JSON.stringify({ type: { event_type: 'node_offline', label: '离线', enabled: true, updated_at: '2026-07-03T00:00:00Z' } }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const created = await createAdminNotificationChannel('admin-pass', {
      name: 'Zeno Telegram',
      destination: '7579942307',
      credential: 'telegram-bot-secret',
      enabled: true,
    })
    const updated = await updateAdminNotificationChannel('admin-pass', 'zeno-telegram', { enabled: false })
    const notificationType = await updateAdminNotificationType('admin-pass', 'node_offline', true)

    expect(created.credentialSet).toBe(true)
    expect(updated.enabled).toBe(false)
    expect(notificationType.enabled).toBe(true)
    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/admin/v1/notification-channels', {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({
        name: 'Zeno Telegram',
          destination: '7579942307',
        credential: 'telegram-bot-secret',
        enabled: true,
      }),
    })
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/admin/v1/notification-channels/zeno-telegram', {
      method: 'PATCH',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({ enabled: false }),
    })
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/admin/v1/notification-types/node_offline', {
      method: 'PATCH',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({ enabled: true }),
    })
    const calls = fetchMock.mock.calls as unknown as Array<[RequestInfo | URL, RequestInit?]>
    expect(String(calls[0]?.[0])).not.toContain('telegram-bot-secret')
  })

  it('tests a notification channel with the admin token and returns a sanitized delivery', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      delivery: {
        id: 9,
        event_type: 'test_notification',
        label: '测试发送',
        node_id: 'admin-test',
        node_name: 'Zeno',
        previous_status: 'test',
        status: 'test',
        channel_id: 'zeno-telegram',
        channel_name: 'Zeno Telegram',
        success: true,
        created_at: '2026-07-03T00:10:00Z',
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const delivery = await testAdminNotificationChannel('admin-pass', 'zeno-telegram')

    expect(delivery.eventType).toBe('test_notification')
    expect(delivery.label).toBe('测试发送')
    expect(delivery.success).toBe(true)
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/notification-channels/zeno-telegram/test', {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
  })

  it('omits a blank notification credential on channel updates to preserve the write-only credential', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      channel: {
        id: 'zeno-telegram',
        name: 'Zeno Telegram Updated',
          destination: '7579942307',
        credential_set: true,
        enabled: true,
        created_at: '2026-07-03T00:00:00Z',
        updated_at: '2026-07-03T00:20:00Z',
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await updateAdminNotificationChannel('admin-pass', 'zeno-telegram', {
      name: 'Zeno Telegram Updated',
      destination: '7579942307',
      credential: '   ',
      enabled: true,
    })

    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/notification-channels/zeno-telegram', {
      method: 'PATCH',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({
        name: 'Zeno Telegram Updated',
          destination: '7579942307',
        enabled: true,
      }),
    })
  })

  it('deletes notification channels with the admin token in X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await deleteAdminNotificationChannel('admin-pass', 'zeno-telegram')

    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/notification-channels/zeno-telegram', {
      method: 'DELETE',
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
  })
})

describe('requestAdminNodeInstallCommand', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('requests an install command from the node edit context without putting the admin token in the URL', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      node_id: 'hytron',
      command: "curl -fsSL 'https://raw.githubusercontent.com/shuijiao1/Zeno-Agent/main/install.sh' | sudo env ZENO_CONTROLLER_URL='https://probe.example.com' ZENO_NODE_ID='hytron' bash",
      commands: {
        linux: "curl -fsSL 'https://raw.githubusercontent.com/shuijiao1/Zeno-Agent/main/install.sh' | sudo env ZENO_CONTROLLER_URL='https://probe.example.com' ZENO_NODE_ID='hytron' bash",
        macos: "curl -fsSL 'https://raw.githubusercontent.com/shuijiao1/Zeno-Agent/main/install.sh' | sudo env ZENO_CONTROLLER_URL='https://probe.example.com' ZENO_NODE_ID='hytron' bash",
        windows: "powershell -NoProfile -ExecutionPolicy Bypass -Command \"$env:ZENO_CONTROLLER_URL='https://probe.example.com'; irm 'https://raw.githubusercontent.com/shuijiao1/Zeno-Agent/main/install.ps1' | iex\"",
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const result = await requestAdminNodeInstallCommand('admin-pass', 'hytron')

    expect(result.nodeId).toBe('hytron')
    expect(result.command).toContain('Zeno-Agent/main/install.sh')
    expect(result.commands.windows).toContain('install.ps1')
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/nodes/hytron/install-command', {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
  })
})
