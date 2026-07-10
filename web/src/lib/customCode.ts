import type { AdminSettings } from '../types'

export const customCodeNodeAttribute = 'data-zeno-custom-code'

let appliedCustomCode = ''

export function extractSafeCustomCSS(customCode: string): string {
  const trimmed = customCode.trim()
  if (trimmed === '') return ''
  if (typeof document === 'undefined') {
    return extractStyleBlocksWithRegex(trimmed) || stripDangerousCSS(trimmed)
  }
  const template = document.createElement('template')
  template.innerHTML = trimmed
  const cssParts: string[] = []
  const content = template.content as DocumentFragment & { querySelectorAll?: DocumentFragment['querySelectorAll'] }
  if (typeof content.querySelectorAll === 'function') {
    content.querySelectorAll('style').forEach((style) => {
      const css = style.textContent?.trim() ?? ''
      if (css !== '') cssParts.push(css)
    })
  } else {
    Array.from(template.content.childNodes).forEach((node) => {
      if (node.nodeName.toLowerCase() === 'style') {
        const css = node.textContent?.trim() ?? ''
        if (css !== '') cssParts.push(css)
      }
    })
  }
  if (cssParts.length === 0 && looksLikeRawCSS(trimmed)) cssParts.push(trimmed)
  return stripDangerousCSS(cssParts.join('\n\n'))
}

function extractStyleBlocksWithRegex(value: string): string {
  const parts = Array.from(value.matchAll(/<style\b[^>]*>([\s\S]*?)<\/style>/gi))
    .map((match) => match[1]?.trim() ?? '')
    .filter(Boolean)
  return stripDangerousCSS(parts.join('\n\n'))
}

function looksLikeRawCSS(value: string): boolean {
  if (/<\/?[a-z][\s\S]*>/i.test(value)) return false
  return /[{}]/.test(value)
}

function stripDangerousCSS(value: string): string {
  return value
    .replace(/@import\b[^;]*;?/gi, '')
    .replace(/url\(\s*(['"]?)\s*javascript:[^)]+\)/gi, 'url(about:blank)')
    .replace(/expression\s*\([^)]*\)/gi, '')
    .trim()
}

export function applyCustomCode(settings: AdminSettings) {
  if (typeof document === 'undefined') return
  const safeCSS = extractSafeCustomCSS(settings.customCode ?? '')
  const currentNodes = document.querySelectorAll(`[${customCodeNodeAttribute}]`)
  if (safeCSS === '') {
    currentNodes.forEach((node) => node.remove())
    appliedCustomCode = ''
    return
  }
  if (safeCSS === appliedCustomCode && currentNodes.length > 0) return
  currentNodes.forEach((node) => node.remove())

  const styleElement = document.createElement('style')
  styleElement.textContent = safeCSS
  styleElement.setAttribute(customCodeNodeAttribute, 'style')
  document.head.appendChild(styleElement)
  appliedCustomCode = safeCSS
}
