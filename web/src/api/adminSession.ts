import { adminCookieSessionMarker } from '../lib/adminToken'

interface ApiAdminLoginResponse {
  username: string
  token?: string
}

interface ApiAdminAccountResponse {
  account: { username: string }
}

export interface AdminLoginData {
  username: string
  token: string
}

export interface AdminAccountData {
  username: string
}

export function adminHeaders(adminToken: string, headers: Record<string, string> = {}): HeadersInit {
  if (adminToken !== '' && adminToken !== adminCookieSessionMarker) {
    return { ...headers, 'X-Admin-Token': adminToken }
  }
  return { ...headers, 'X-Zeno-CSRF': '1' }
}

export async function loginAdmin(username: string, password: string): Promise<AdminLoginData> {
  const response = await fetch('/api/admin/v1/login', {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-Zeno-CSRF': '1',
    },
    body: JSON.stringify({ username, password }),
  })
  if (!response.ok) {
    throw new Error(response.status === 429 ? '登录失败次数过多，请稍后再试。' : `admin login failed: ${response.status}`)
  }
  const data = await response.json() as ApiAdminLoginResponse
  return { username: data.username, token: adminCookieSessionMarker }
}

export async function fetchAdminAccount(adminToken: string, signal?: AbortSignal): Promise<AdminAccountData> {
  const response = await fetch('/api/admin/v1/account', {
    signal,
    headers: adminHeaders(adminToken, { Accept: 'application/json' }),
  })
  if (!response.ok) throw new Error(`admin account failed: ${response.status}`)
  const data = await response.json() as ApiAdminAccountResponse
  return { username: data.account.username }
}

export async function logoutAdmin(adminToken: string): Promise<void> {
  const response = await fetch('/api/admin/v1/logout', {
    method: 'POST',
    headers: adminHeaders(adminToken),
  })
  if (!response.ok) throw new Error(`admin logout failed: ${response.status}`)
}

export async function updateAdminAccount(adminToken: string, username: string, currentPassword: string, newPassword: string): Promise<AdminLoginData> {
  const response = await fetch('/api/admin/v1/account', {
    method: 'POST',
    headers: adminHeaders(adminToken, { Accept: 'application/json', 'Content-Type': 'application/json' }),
    body: JSON.stringify({ username, current_password: currentPassword, new_password: newPassword }),
  })
  if (!response.ok) {
    throw new Error(`admin account update failed: ${response.status}`)
  }
  const data = await response.json() as ApiAdminLoginResponse
  return { username: data.username, token: adminToken === adminCookieSessionMarker ? adminCookieSessionMarker : (data.token ?? '') }
}
