import { afterEach, describe, expect, it, vi } from 'vitest'
import { createAdminNode, createAdminNotificationChannel, createAdminProbeTarget, deleteAdminNotificationChannel, fetchAdminNodes, fetchAdminNotificationChannels, fetchAdminNotificationDeliveries, fetchAdminNotificationTypes, fetchAdminProbeTargets, normalizeAdminNodes, normalizeAdminNotificationChannels, normalizeAdminNotificationDeliveries, normalizeAdminNotificationTypes, normalizeAdminProbeTargets, normalizeNodeLatency, normalizeNodeState, normalizeSummary, requestAdminNodeInstallCommand, testAdminNotificationChannel, updateAdminNode, updateAdminNotificationChannel, updateAdminNotificationType, updateAdminProbeTarget } from './client'

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
          net_in_speed_bps: 1024,
          net_out_speed_bps: 2048,
          net_in_total_bytes: 4096,
          net_out_total_bytes: 8192,
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
        },
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
    expect(summary.nodes[0].monthlyBillableBytes).toBe(1000)
    expect(summary.nodes[0].latencySummary?.targetName).toBe('Google')
    expect(summary.nodes[0].osVersion).toBe('13')
    expect(summary.nodes[0].kernel).toBe('6.12.0')
    expect(summary.nodes[0].virtualization).toBe('kvm')
    expect(summary.nodes[0].cpuModel).toBe('AMD EPYC')
    expect(summary.latencyPoints[0].targetId).toBe('google')
    expect(summary.latencyPoints[0].medianMs).toBeNull()
    expect(summary.latencyPoints[0].lossPercent).toBe(100)
  })

  it('normalizes null collections from empty preview stores', () => {
    const summary = normalizeSummary({
      nodes: null,
      latency_points: null,
    })

    expect(summary.nodes).toEqual([])
    expect(summary.latencyPoints).toEqual([])
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
          enabled: true,
          assignments: null as never,
        },
      ],
    })

    expect(data.targets[0].assignments).toEqual([])
  })
})

describe('normalizeAdminNotifications', () => {
  it('maps channels and types without credential values', () => {
    const channels = normalizeAdminNotificationChannels({
      channels: [
        {
          id: 'zeno-webhook',
          name: 'Zeno Webhook',
          type: 'webhook',
          destination: 'https://example.com/notify',
          credential_set: true,
          enabled: false,
          created_at: '2026-07-03T00:00:00Z',
          updated_at: '2026-07-03T00:00:00Z',
        },
      ],
    })
    const types = normalizeAdminNotificationTypes({
      types: [
        { event_type: 'node_online', label: '上线', enabled: true, updated_at: '2026-07-03T00:00:00Z' },
      ],
    })

    expect(channels.channels[0].credentialSet).toBe(true)
    expect(channels.channels[0]).not.toHaveProperty('credential')
    expect(types.types[0].eventType).toBe('node_online')
    expect(types.types[0].enabled).toBe(true)
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


describe('fetchAdminNotifications', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('fetches notification channels and types with X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async (url: string | URL | Request) => {
      if (String(url).includes('notification-channels')) {
        return new Response(JSON.stringify({ channels: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      return new Response(JSON.stringify({ types: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await fetchAdminNotificationChannels('admin-pass')
    await fetchAdminNotificationTypes('admin-pass')

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/admin/v1/notification-channels', {
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/admin/v1/notification-types', {
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
        disabled: true,
        billing_mode: 'both',
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
      monthlyQuotaBytes: 123456789,
      disabled: true,
    })

    expect(node.displayName).toBe('Hytron Edited')
    expect(node.disabled).toBe(true)
    expect(node.monthlyQuotaBytes).toBe(123456789)
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
        monthly_quota_bytes: 123456789,
        disabled: true,
      }),
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
        billing_mode: 'both',
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
      monthlyQuotaBytes: 1099511627776,
    })

    expect(node.id).toBe('new-server-a1b2c3d4')
    expect(node.status).toBe('no_data')
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
        enabled: true,
      }),
    })
  })
})

describe('admin notification deliveries', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('normalizes delivery history and fetches it with X-Admin-Token only', async () => {
    const apiPayload = {
      deliveries: [
        {
          id: 7,
          event_type: 'node_online',
          label: '上线',
          node_id: 'hytron',
          node_name: 'Hytron',
          previous_status: 'no_data',
          status: 'online',
          channel_id: 'zeno-webhook',
          channel_name: 'Zeno Webhook',
          channel_type: 'webhook' as const,
          success: false,
          error: 'webhook returned status 500',
          created_at: '2026-07-03T00:05:00Z',
        },
      ],
    }
    const normalized = normalizeAdminNotificationDeliveries(apiPayload)
    expect(normalized.deliveries[0].nodeName).toBe('Hytron')
    expect(normalized.deliveries[0].success).toBe(false)
    expect(normalized.deliveries[0].error).toBe('webhook returned status 500')

    const fetchMock = vi.fn(async () => new Response(JSON.stringify(apiPayload), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const fetched = await fetchAdminNotificationDeliveries('admin-pass')
    expect(fetched.deliveries[0].channelName).toBe('Zeno Webhook')
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/notification-deliveries', {
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
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
            id: 'zeno-webhook',
            name: 'Zeno Webhook',
            type: 'webhook',
            destination: 'https://example.com/notify',
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
            id: 'zeno-webhook',
            name: 'Zeno Webhook',
            type: 'webhook',
            destination: 'https://example.com/notify',
            credential_set: true,
            enabled: false,
            created_at: '2026-07-03T00:00:00Z',
            updated_at: '2026-07-03T00:00:00Z',
          },
        }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      return new Response(JSON.stringify({ type: { event_type: 'node_online', label: '上线', enabled: true, updated_at: '2026-07-03T00:00:00Z' } }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const created = await createAdminNotificationChannel('admin-pass', {
      name: 'Zeno Webhook',
      type: 'webhook',
      destination: 'https://example.com/notify',
      credential: 'webhook-secret',
      enabled: true,
    })
    const updated = await updateAdminNotificationChannel('admin-pass', 'zeno-webhook', { enabled: false })
    const notificationType = await updateAdminNotificationType('admin-pass', 'node_online', true)

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
        name: 'Zeno Webhook',
        type: 'webhook',
        destination: 'https://example.com/notify',
        credential: 'webhook-secret',
        enabled: true,
      }),
    })
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/admin/v1/notification-channels/zeno-webhook', {
      method: 'PATCH',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({ enabled: false }),
    })
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/admin/v1/notification-types/node_online', {
      method: 'PATCH',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({ enabled: true }),
    })
    expect(String(fetchMock.mock.calls[0][0])).not.toContain('webhook-secret')
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
        channel_id: 'zeno-webhook',
        channel_name: 'Zeno Webhook',
        channel_type: 'webhook',
        success: true,
        created_at: '2026-07-03T00:10:00Z',
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const delivery = await testAdminNotificationChannel('admin-pass', 'zeno-webhook')

    expect(delivery.eventType).toBe('test_notification')
    expect(delivery.label).toBe('测试发送')
    expect(delivery.success).toBe(true)
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/notification-channels/zeno-webhook/test', {
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
        id: 'zeno-webhook',
        name: 'Zeno Webhook Updated',
        type: 'webhook',
        destination: 'https://example.com/updated',
        credential_set: true,
        enabled: true,
        created_at: '2026-07-03T00:00:00Z',
        updated_at: '2026-07-03T00:20:00Z',
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await updateAdminNotificationChannel('admin-pass', 'zeno-webhook', {
      name: 'Zeno Webhook Updated',
      type: 'webhook',
      destination: 'https://example.com/updated',
      credential: '   ',
      enabled: true,
    })

    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/notification-channels/zeno-webhook', {
      method: 'PATCH',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({
        name: 'Zeno Webhook Updated',
        type: 'webhook',
        destination: 'https://example.com/updated',
        enabled: true,
      }),
    })
  })

  it('deletes notification channels with the admin token in X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await deleteAdminNotificationChannel('admin-pass', 'zeno-webhook')

    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/notification-channels/zeno-webhook', {
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
      command: "curl -fsSL 'https://probe.example.com/api/public/v1/agent/linux-amd64'",
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const result = await requestAdminNodeInstallCommand('admin-pass', 'hytron')

    expect(result.nodeId).toBe('hytron')
    expect(result.command).toContain('/api/public/v1/agent/linux-amd64')
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/nodes/hytron/install-command', {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
  })
})
