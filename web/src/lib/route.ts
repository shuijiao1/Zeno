export type DashboardRoute =
  | { kind: 'home' }
  | { kind: 'admin' }
  | { kind: 'node'; nodeId: string }

export function parseDashboardRoute(pathname: string): DashboardRoute {
  const normalized = pathname || '/'
  if (normalized === '/' || normalized === '/index.html') {
    return { kind: 'home' }
  }

  if (normalized === '/dashboard' || normalized === '/dashboard/') {
    return { kind: 'admin' }
  }

  const match = normalized.match(/^\/server\/([^/]+)\/?$/)
  if (match) {
    return { kind: 'node', nodeId: decodeURIComponent(match[1]) }
  }

  return { kind: 'home' }
}

export function nodePath(nodeId: string): string {
  return `/server/${encodeURIComponent(nodeId)}`
}
