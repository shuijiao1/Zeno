import { LatencyChart } from './components/LatencyChart'
import { ServerCard } from './components/ServerCard'
import { mockLatencyPoints, mockNodes } from './mock/data'

export function App() {
  const onlineCount = mockNodes.filter((node) => node.status === 'online').length

  return (
    <main className="app-shell">
      <section className="hero">
        <div>
          <p className="eyebrow">JiaoProbe / 饺探</p>
          <h1>水饺服务器状态</h1>
          <p className="hero__subcopy">Mock 数据阶段：先锁定首页大卡片和 Nezha-like 延迟图，不接后端、不改旧系统。</p>
        </div>
        <div className="hero__status">
          <strong>{onlineCount}/{mockNodes.length}</strong>
          <span>online</span>
        </div>
      </section>

      <section className="cards-grid" aria-label="server cards">
        {mockNodes.map((node) => <ServerCard key={node.id} node={node} />)}
      </section>

      <LatencyChart points={mockLatencyPoints} />
    </main>
  )
}
