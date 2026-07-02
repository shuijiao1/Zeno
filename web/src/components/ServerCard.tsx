import type { HomeCardNode } from '../types'
import { formatBps, formatBytes, formatLatency, formatPercent } from '../lib/format'
import { ResourceBar } from './ResourceBar'

interface ServerCardProps {
  node: HomeCardNode
  isSelected?: boolean
  onSelect?: (nodeId: string) => void
}

const osIcon: Record<HomeCardNode['os'], string> = {
  debian: '◆',
  ubuntu: '●',
  centos: '◈',
  alpine: '▲',
  linux: '⬢',
  unknown: '○',
}

function flag(countryCode?: string): string {
  if (!countryCode || countryCode.length !== 2) return '🏳️'
  const base = 127397
  return countryCode
    .toUpperCase()
    .split('')
    .map((char) => String.fromCodePoint(char.charCodeAt(0) + base))
    .join('')
}

function percent(used: number | null, total: number | null): number | null {
  if (used === null || total === null || total <= 0) return null
  return (used / total) * 100
}

export function ServerCard({ node, isSelected = false, onSelect }: ServerCardProps) {
  const monthlyPercent = percent(node.monthlyBillableBytes, node.monthlyQuotaBytes)
  const latency = node.latencySummary

  return (
    <article className={`server-card status-${node.status}${isSelected ? ' is-selected' : ''}`}>
      <header className="server-card__header">
        <div className="server-card__icon" aria-hidden="true">{osIcon[node.os]}</div>
        <div className="server-card__title-block">
          <div className="server-card__title-line">
            <span className="status-dot" aria-label={node.status} />
            <span className="flag">{flag(node.countryCode)}</span>
            <h2>{node.displayName}</h2>
          </div>
          <p>{node.subtitle ?? 'No subtitle'}</p>
        </div>
        <span className="status-pill">{node.status.replace('_', ' ')}</span>
      </header>

      <section className="server-card__bars" aria-label={`${node.displayName} resources`}>
        <ResourceBar label="CPU" percent={node.cpuPercent} valueText={formatPercent(node.cpuPercent)} />
        <ResourceBar label="内存" percent={percent(node.memoryUsedBytes, node.memoryTotalBytes)} usedBytes={node.memoryUsedBytes} totalBytes={node.memoryTotalBytes} />
        <ResourceBar label="硬盘" percent={percent(node.diskUsedBytes, node.diskTotalBytes)} usedBytes={node.diskUsedBytes} totalBytes={node.diskTotalBytes} />
        <ResourceBar label="月流量" percent={monthlyPercent} usedBytes={node.monthlyBillableBytes} totalBytes={node.monthlyQuotaBytes} />
      </section>

      <section className="server-card__traffic" aria-label={`${node.displayName} traffic`}>
        <div><span>↓</span><strong>{formatBps(node.netInSpeedBps)}</strong><em>当前下载</em></div>
        <div><span>↑</span><strong>{formatBps(node.netOutSpeedBps)}</strong><em>当前上传</em></div>
        <div><span>↙</span><strong>{formatBytes(node.netInTotalBytes)}</strong><em>总接收</em></div>
        <div><span>↗</span><strong>{formatBytes(node.netOutTotalBytes)}</strong><em>总发送</em></div>
      </section>

      <footer className="server-card__latency">
        <span className="latency-target">{latency?.targetName ?? 'No target'}</span>
        <strong>{formatLatency(latency?.medianMs)}</strong>
        <span className={(latency?.lossPercent ?? 0) > 0 ? 'loss is-loss' : 'loss'}>
          loss {formatPercent(latency?.lossPercent)}
        </span>
        {onSelect && (
          <button className="detail-button" type="button" aria-pressed={isSelected} onClick={() => onSelect(node.id)}>
            {isSelected ? '已选中' : '详情'}
          </button>
        )}
      </footer>
    </article>
  )
}
