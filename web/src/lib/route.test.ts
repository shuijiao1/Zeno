import { describe, expect, it } from 'vitest'
import { parseDashboardRoute } from './route'

describe('parseDashboardRoute', () => {
  it('keeps the public home page on slash-like paths', () => {
    expect(parseDashboardRoute('/')).toEqual({ kind: 'home' })
    expect(parseDashboardRoute('/index.html')).toEqual({ kind: 'home' })
  })

  it('routes the login/backend entry to the admin dashboard shell', () => {
    expect(parseDashboardRoute('/dashboard')).toEqual({ kind: 'admin' })
    expect(parseDashboardRoute('/dashboard/')).toEqual({ kind: 'admin' })
  })

  it('extracts decoded node ids from Kulin-style server detail URLs', () => {
    expect(parseDashboardRoute('/server/sharon')).toEqual({ kind: 'node', nodeId: 'sharon' })
    expect(parseDashboardRoute('/server/DataWave%20HK')).toEqual({ kind: 'node', nodeId: 'DataWave HK' })
  })

  it('extracts decoded service target ids from service history URLs', () => {
    expect(parseDashboardRoute('/service/google')).toEqual({ kind: 'service', targetId: 'google' })
    expect(parseDashboardRoute('/service/Akari%20HK')).toEqual({ kind: 'service', targetId: 'Akari HK' })
  })
})
