export const adminTokenStorageKey = 'zeno_admin_token'
export const adminTokenStoredAtKey = 'zeno_admin_token_saved_at'
export const adminTokenMaxAgeMs = 24 * 60 * 60 * 1000

// This is only an in-process authenticated-state marker. It is not a
// credential and is never sent as X-Admin-Token or persisted in browser
// storage; the replayable session is held solely by the HttpOnly cookie.
export const adminCookieSessionMarker = '__zeno_http_only_cookie_session__'

let memoryAdminSession = false
let memoryAdminTokenGeneration = 0

export type AdminTokenIdentity = Readonly<{
  token: string
  generation: number
}>

function storage(kind: 'sessionStorage' | 'localStorage'): Storage | null {
  if (typeof window === 'undefined') return null
  try {
    return window[kind] ?? null
  } catch {
    return null
  }
}

function removeLegacyReplayableTokens() {
  for (const kind of ['sessionStorage', 'localStorage'] as const) {
    try {
      const store = storage(kind)
      store?.removeItem(adminTokenStorageKey)
      store?.removeItem(adminTokenStoredAtKey)
    } catch {}
  }
}

export function loadStoredAdminToken(): string {
  removeLegacyReplayableTokens()
  return memoryAdminSession ? adminCookieSessionMarker : ''
}

export function rememberAdminToken(_token = adminCookieSessionMarker) {
  removeLegacyReplayableTokens()
  memoryAdminTokenGeneration += 1
  memoryAdminSession = true
}

export function clearStoredAdminToken() {
  memoryAdminTokenGeneration += 1
  memoryAdminSession = false
  removeLegacyReplayableTokens()
}

export function captureAdminTokenIdentity(token: string): AdminTokenIdentity {
  return Object.freeze({ token, generation: memoryAdminTokenGeneration })
}

export function isAdminTokenIdentityCurrent(identity: AdminTokenIdentity): boolean {
  return memoryAdminSession
    && identity.token === adminCookieSessionMarker
    && identity.generation === memoryAdminTokenGeneration
}

export function clearStoredAdminTokenIfCurrent(identity: AdminTokenIdentity): boolean {
  if (!isAdminTokenIdentityCurrent(identity)) return false
  clearStoredAdminToken()
  return true
}
