import { describe, expect, it } from 'vitest'
import { formatBytes, formatBps, formatPercent } from './format'

describe('formatBytes', () => {
  it('formats nullish values as no data', () => {
    expect(formatBytes(null)).toBe('No data')
    expect(formatBytes(undefined)).toBe('No data')
  })

  it('formats binary units compactly', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1536)).toBe('1.5 KiB')
    expect(formatBytes(1073741824)).toBe('1.0 GiB')
  })
})

describe('formatBps', () => {
  it('formats network speeds per second', () => {
    expect(formatBps(2048)).toBe('2.0 KiB/s')
  })
})

describe('formatPercent', () => {
  it('formats percentages with one decimal place', () => {
    expect(formatPercent(12.345)).toBe('12.3%')
  })

  it('uses no data for nullish values', () => {
    expect(formatPercent(null)).toBe('No data')
  })
})
