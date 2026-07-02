import { describe, expect, it } from 'vitest'
import { normalizeSummary } from './client'

describe('normalizeSummary', () => {
  it('maps controller snake_case JSON into frontend camelCase models', () => {
    const summary = normalizeSummary({
      nodes: [
        {
          id: 'hytron',
          display_name: 'Hytron',
          status: 'online',
          os: 'debian',
          country_code: 'HK',
          subtitle: 'Hong Kong',
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
    expect(summary.nodes[0].countryCode).toBe('HK')
    expect(summary.nodes[0].monthlyBillableBytes).toBe(1000)
    expect(summary.nodes[0].latencySummary?.targetName).toBe('Google')
    expect(summary.latencyPoints[0].targetId).toBe('google')
    expect(summary.latencyPoints[0].medianMs).toBeNull()
    expect(summary.latencyPoints[0].lossPercent).toBe(100)
  })
})
