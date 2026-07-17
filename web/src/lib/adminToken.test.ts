import { afterEach, describe, expect, it } from 'vitest'
import { adminCookieSessionMarker, adminTokenStorageKey, adminTokenStoredAtKey, captureAdminTokenIdentity, clearStoredAdminToken, clearStoredAdminTokenIfCurrent, loadStoredAdminToken, rememberAdminToken } from './adminToken'

function makeStorage() {
  const values = new Map<string, string>()
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  } satisfies Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>
}

function installWindowStorage() {
  const previousWindow = globalThis.window
  const localStorage = makeStorage()
  const sessionStorage = makeStorage()
  Object.defineProperty(globalThis, 'window', { configurable: true, value: { localStorage, sessionStorage } })
  return { localStorage, sessionStorage, restore: () => Object.defineProperty(globalThis, 'window', { configurable: true, value: previousWindow }) }
}

afterEach(() => clearStoredAdminToken())

describe('HttpOnly admin session state', () => {
  it('removes replayable legacy tokens and persists no new token in browser storage', () => {
    const { localStorage, sessionStorage, restore } = installWindowStorage()
    try {
      localStorage.setItem(adminTokenStorageKey, 'legacy-local-token')
      localStorage.setItem(adminTokenStoredAtKey, '123')
      sessionStorage.setItem(adminTokenStorageKey, 'legacy-session-token')
      sessionStorage.setItem(adminTokenStoredAtKey, '456')

      expect(loadStoredAdminToken()).toBe('')
      rememberAdminToken('server-token-must-not-be-kept')

      expect(loadStoredAdminToken()).toBe(adminCookieSessionMarker)
      expect(localStorage.getItem(adminTokenStorageKey)).toBeNull()
      expect(sessionStorage.getItem(adminTokenStorageKey)).toBeNull()
      expect(localStorage.getItem(adminTokenStoredAtKey)).toBeNull()
      expect(sessionStorage.getItem(adminTokenStoredAtKey)).toBeNull()
    } finally {
      restore()
    }
  })

  it('treats browser storage access failures as an empty or in-memory-only session', () => {
    const previousWindow = globalThis.window
    const blockedStorage = {
      getItem: () => { throw new DOMException('blocked', 'SecurityError') },
      setItem: () => { throw new DOMException('blocked', 'SecurityError') },
      removeItem: () => { throw new DOMException('blocked', 'SecurityError') },
    }
    Object.defineProperty(globalThis, 'window', { configurable: true, value: { localStorage: blockedStorage, sessionStorage: blockedStorage } })
    try {
      expect(loadStoredAdminToken()).toBe('')
      expect(() => rememberAdminToken()).not.toThrow()
      expect(loadStoredAdminToken()).toBe(adminCookieSessionMarker)
      expect(() => clearStoredAdminToken()).not.toThrow()
    } finally {
      Object.defineProperty(globalThis, 'window', { configurable: true, value: previousWindow })
    }
  })

  it('uses generations so a late 401 cannot clear a renewed cookie session marker', () => {
    const { restore } = installWindowStorage()
    try {
      rememberAdminToken()
      const oldRequestIdentity = captureAdminTokenIdentity(adminCookieSessionMarker)
      rememberAdminToken()

      expect(clearStoredAdminTokenIfCurrent(oldRequestIdentity)).toBe(false)
      expect(loadStoredAdminToken()).toBe(adminCookieSessionMarker)
    } finally {
      restore()
    }
  })

  it('clears the current in-memory marker on a 401 without leaving browser credentials', () => {
    const { localStorage, sessionStorage, restore } = installWindowStorage()
    try {
      rememberAdminToken()
      const identity = captureAdminTokenIdentity(adminCookieSessionMarker)

      expect(clearStoredAdminTokenIfCurrent(identity)).toBe(true)
      expect(loadStoredAdminToken()).toBe('')
      expect(localStorage.getItem(adminTokenStorageKey)).toBeNull()
      expect(sessionStorage.getItem(adminTokenStorageKey)).toBeNull()
    } finally {
      restore()
    }
  })
})
