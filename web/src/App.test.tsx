import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { HomeOverviewPanel } from './App'

describe('HomeOverviewPanel', () => {
  it('renders one combined overview card instead of four separate cards', () => {
    const html = renderToStaticMarkup(
      <HomeOverviewPanel
        totalCount={11}
        onlineCount={9}
        offlineCount={2}
        totalUp={1024}
        totalDown={2048}
        upSpeed={128}
        downSpeed={256}
      />,
    )

    expect(html).toContain('overview-card--combined')
    expect(html.match(/overview-metric/g)).toHaveLength(4)
    expect(html).toContain('服务器总数')
    expect(html).toContain('在线服务器')
    expect(html).toContain('离线服务器')
    expect(html).toContain('网络')
  })
})
