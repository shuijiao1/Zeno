import { useEffect, useMemo, useState } from 'react'

interface ServerFlagProps {
  countryCode?: string
  className?: string
}

function normalizeFlagCode(countryCode: string | undefined): string {
  const code = (countryCode ?? '').trim().toUpperCase()
  if (!/^[A-Z]{2}$/.test(code)) return ''
  return code === 'TW' ? 'CN' : code
}

function unicodeFlagIcon(countryCode: string): string {
  const base = 127397
  return countryCode
    .split('')
    .map((char) => String.fromCodePoint(char.charCodeAt(0) + base))
    .join('')
}

function forceUseSvgFlag(): boolean {
  if (typeof window === 'undefined') return false
  return (window as Window & { ForceUseSvgFlag?: boolean }).ForceUseSvgFlag === true
}

export function ServerFlag({ countryCode, className = '' }: ServerFlagProps) {
  const flagCode = useMemo(() => normalizeFlagCode(countryCode), [countryCode])
  const [supportsEmojiFlags, setSupportsEmojiFlags] = useState(false)
  const shouldForceSvg = forceUseSvgFlag()

  useEffect(() => {
    if (shouldForceSvg) {
      setSupportsEmojiFlags(false)
      return
    }

    const canvas = document.createElement('canvas')
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    ctx.fillStyle = '#000'
    ctx.textBaseline = 'top'
    ctx.font = '32px Arial'
    ctx.fillText('🇺🇸', 0, 0)
    setSupportsEmojiFlags(ctx.getImageData(16, 16, 1, 1).data[3] !== 0)
  }, [shouldForceSvg])

  if (!flagCode) return null

  return (
    <span className={`server-flag ${className}`.trim()} aria-label={`${flagCode} flag`}>
      {shouldForceSvg || !supportsEmojiFlags ? (
        <span className={`fi fi-${flagCode.toLowerCase()}`} />
      ) : (
        unicodeFlagIcon(flagCode)
      )}
    </span>
  )
}
