// @ts-nocheck
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { describe, expect, it } from 'vitest'

const stylesPath = join(dirname(fileURLToPath(import.meta.url)), 'styles.css')
const styles = readFileSync(stylesPath, 'utf8')

describe('mobile latency target layout', () => {
  it('keeps latency target buttons compact at three per row on phones', () => {
    expect(styles).toContain('@media (max-width: 767px)')
    expect(styles).toContain('.latency-target-grid button')
    expect(styles).toContain('flex: 0 0 calc(100% / 3)')
  })
})

describe('state history layout', () => {
  it('keeps resource history as compact cards and stacks them on phones', () => {
    expect(styles).toContain('.state-history-grid')
    expect(styles).toContain('grid-template-columns: repeat(4, minmax(0, 1fr))')
    expect(styles).toContain('grid-template-columns: repeat(2, minmax(0, 1fr))')
  })
})
