export type DashboardRoute =
  | { kind: 'home' }
  | { kind: 'node'; nodeId: string }

export function parseDashboardRoute(pathname: string): DashboardRoute {
  const normalized = pathname || '/'
  if (normalized === '/' || normalized === '/index.html') {
    return { kind: 'home' }
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
