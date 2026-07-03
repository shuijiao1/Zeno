import { describe, expect, it } from 'vitest'
import { normalizeNodeLatency, normalizeNodeState, normalizeSummary } from './client'

describe('normalizeSummary', () => {
  it('maps controller snake_case JSON into frontend camelCase models', () => {
    const summary = normalizeSummary({
      nodes: [
        {
          id: 'hytron',
          display_name: 'Hytron',
          status: 'online',
          os: 'debian',
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
    expect(summary.latencyPoints[0].targetId).toBe('google')
    expect(summary.latencyPoints[0].medianMs).toBeNull()
    expect(summary.latencyPoints[0].lossPercent).toBe(100)
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

    expect(data.nodeId).toBe('hytron')
    expect(data.range).toBe('1h')
    expect(data.points[0].cpuPercent).toBe(18.75)
    expect(data.points[0].memoryUsedBytes).toBe(4096)
    expect(data.points[0].netOutSpeedBps).toBe(256)
    expect(data.points[0].uptimeSeconds).toBe(3601)
  })
})
