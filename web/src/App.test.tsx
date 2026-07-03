import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { AdminDashboard, HomeTopPanel } from './App'

const overviewProps = {
  totalCount: 11,
  onlineCount: 9,
  offlineCount: 2,
  totalUp: 1024,
  totalDown: 2048,
  upSpeed: 128,
  downSpeed: 256,
}

describe('HomeTopPanel', () => {
  it('keeps every homepage top control inside one card above server cards', () => {
    const html = renderToStaticMarkup(
      <HomeTopPanel
        {...overviewProps}
        onHome={() => {}}
        onAdmin={() => {}}
      />,
    )

    expect(html).toContain('home-top-card')
    expect(html).toContain('dashboard actions')
    expect(html).toContain('水饺的探针')
    expect(html).toContain('服务器总数')
    expect(html).toContain('在线服务器')
    expect(html).toContain('离线服务器')
    expect(html).toContain('网络')
    expect(html.match(/overview-metric/g)).toHaveLength(4)
    expect(html.indexOf('水饺的探针')).toBeLessThan(html.indexOf('服务器总数'))
    expect(html).not.toContain('overview-card--combined')
  })
})

describe('AdminDashboard', () => {
  it('uses the same card shell and action style as the public front page', () => {
    const html = renderToStaticMarkup(<AdminDashboard onHome={() => {}} />)

    expect(html).toContain('home-top-card')
    expect(html).toContain('admin-panel')
    expect(html).toContain('JiaoProbe 后台')
    expect(html).toContain('沿用前台卡片风格')
    expect(html).toContain('dashboard actions')
  })
})
