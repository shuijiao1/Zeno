export const adminTokenStorageKey = 'zeno_admin_token'
export const adminTokenStoredAtKey = 'zeno_admin_token_saved_at'
export const adminTokenMaxAgeMs = 24 * 60 * 60 * 1000

let memoryAdminToken = ''
let memoryAdminTokenStoredAt = 0

function nowMs(): number {
  return Date.now()
}

function storage(kind: 'sessionStorage' | 'localStorage'): Storage | null {
  if (typeof window === 'undefined') return null
  try {
    return window[kind] ?? null
  } catch {
    return null
  }
}

function isFresh(storedAt: number, now = nowMs()): boolean {
  return Number.isFinite(storedAt) && storedAt > 0 && now - storedAt <= adminTokenMaxAgeMs
}

function rememberInMemory(token: string, storedAt = nowMs()) {
  memoryAdminToken = token
  memoryAdminTokenStoredAt = storedAt
}

function readTokenFromStorage(store: Storage | null): { token: string; storedAt: number } | null {
  if (!store) return null
  try {
    const token = store.getItem(adminTokenStorageKey) ?? ''
    if (token === '') return null
    const rawStoredAt = Number(store.getItem(adminTokenStoredAtKey) ?? '')
    const storedAt = Number.isFinite(rawStoredAt) && rawStoredAt > 0 ? rawStoredAt : nowMs()
    return { token, storedAt }
  } catch {
    return null
  }
}

export function loadStoredAdminToken(): string {
  if (memoryAdminToken !== '') {
    if (isFresh(memoryAdminTokenStoredAt)) return memoryAdminToken
    clearStoredAdminToken()
    return ''
  }

  const session = storage('sessionStorage')
  const sessionToken = readTokenFromStorage(session)
  if (sessionToken) {
    if (!isFresh(sessionToken.storedAt)) {
      clearStoredAdminToken()
      return ''
    }
    rememberInMemory(sessionToken.token, sessionToken.storedAt)
    return sessionToken.token
  }

  // One-time compatibility migration: older builds kept the admin token in
  // localStorage for up to 24h. Move a still-fresh legacy token to
  // sessionStorage/in-memory and remove the long-lived copy.
  const legacy = storage('localStorage')
  const legacyToken = readTokenFromStorage(legacy)
  if (!legacyToken) return ''
  if (!isFresh(legacyToken.storedAt)) {
    clearStoredAdminToken()
    return ''
  }
  rememberAdminToken(legacyToken.token, legacyToken.storedAt)
  try {
    legacy?.removeItem(adminTokenStorageKey)
    legacy?.removeItem(adminTokenStoredAtKey)
  } catch {}
  return legacyToken.token
}

export function rememberAdminToken(token: string, storedAt = nowMs()) {
  if (token === '') {
    clearStoredAdminToken()
    return
  }
  rememberInMemory(token, storedAt)
  const session = storage('sessionStorage')
  try {
    session?.setItem(adminTokenStorageKey, token)
    session?.setItem(adminTokenStoredAtKey, String(storedAt))
  } catch {}
  try {
    const legacy = storage('localStorage')
    legacy?.removeItem(adminTokenStorageKey)
    legacy?.removeItem(adminTokenStoredAtKey)
  } catch {}
}

export function clearStoredAdminToken() {
  memoryAdminToken = ''
  memoryAdminTokenStoredAt = 0
  for (const kind of ['sessionStorage', 'localStorage'] as const) {
    try {
      const store = storage(kind)
      store?.removeItem(adminTokenStorageKey)
      store?.removeItem(adminTokenStoredAtKey)
    } catch {}
  }
}
