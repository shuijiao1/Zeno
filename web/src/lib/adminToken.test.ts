import { afterEach, describe, expect, it } from 'vitest'
import { adminTokenStorageKey, adminTokenStoredAtKey, clearStoredAdminToken, loadStoredAdminToken, rememberAdminToken } from './adminToken'

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
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: { localStorage, sessionStorage },
  })
  return { localStorage, sessionStorage, restore: () => Object.defineProperty(globalThis, 'window', { configurable: true, value: previousWindow }) }
}

afterEach(() => {
  clearStoredAdminToken()
})

describe('admin token storage', () => {
  it('stores new admin tokens in sessionStorage instead of long-lived localStorage', () => {
    const { localStorage, sessionStorage, restore } = installWindowStorage()
    try {
      const storedAt = Date.now()
      rememberAdminToken('session-token', storedAt)

      expect(sessionStorage.getItem(adminTokenStorageKey)).toBe('session-token')
      expect(sessionStorage.getItem(adminTokenStoredAtKey)).toBe(String(storedAt))
      expect(localStorage.getItem(adminTokenStorageKey)).toBeNull()
      expect(loadStoredAdminToken()).toBe('session-token')
    } finally {
      restore()
    }
  })

  it('migrates fresh legacy localStorage tokens into sessionStorage and removes the legacy copy', () => {
    const { localStorage, sessionStorage, restore } = installWindowStorage()
    try {
      const storedAt = Date.now()
      localStorage.setItem(adminTokenStorageKey, 'legacy-token')
      localStorage.setItem(adminTokenStoredAtKey, String(storedAt))

      expect(loadStoredAdminToken()).toBe('legacy-token')
      expect(sessionStorage.getItem(adminTokenStorageKey)).toBe('legacy-token')
      expect(localStorage.getItem(adminTokenStorageKey)).toBeNull()
    } finally {
      restore()
    }
  })

  it('treats browser storage access failures as an empty session', () => {
    const previousWindow = globalThis.window
    const blockedStorage = {
      getItem: () => { throw new DOMException('blocked', 'SecurityError') },
      setItem: () => { throw new DOMException('blocked', 'SecurityError') },
      removeItem: () => { throw new DOMException('blocked', 'SecurityError') },
    }
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: { localStorage: blockedStorage, sessionStorage: blockedStorage },
    })
    try {
      expect(loadStoredAdminToken()).toBe('')
      expect(() => rememberAdminToken('token')).not.toThrow()
      expect(() => clearStoredAdminToken()).not.toThrow()
    } finally {
      Object.defineProperty(globalThis, 'window', { configurable: true, value: previousWindow })
    }
  })
})
