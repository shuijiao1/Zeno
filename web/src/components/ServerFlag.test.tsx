import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { ServerFlag } from './ServerFlag'

describe('ServerFlag', () => {
  it('renders Kulin-style SVG fallback markup during server render', () => {
    const html = renderToStaticMarkup(<ServerFlag countryCode="HK" className="node-flag" />)

    expect(html).toContain('class="server-flag node-flag"')
    expect(html).toContain('aria-label="HK flag"')
    expect(html).toContain('class="fi fi-hk"')
  })

  it('normalizes TW to CN like Kulin', () => {
    const html = renderToStaticMarkup(<ServerFlag countryCode="TW" />)

    expect(html).toContain('aria-label="CN flag"')
    expect(html).toContain('class="fi fi-cn"')
  })
})
