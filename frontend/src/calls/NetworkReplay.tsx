import { useEffect, useId, useMemo, useState } from 'react'
import type { CallDetail } from '../api/types'
import { formatMs } from '../money/format'
import { endReasonLabel } from './labels'
import { Button } from '../ui'
import { OutcomeBadge } from './OutcomeBadge'
import { mapPoint, replayDuration, replaySpans } from './network'
import './network.css'

export default function NetworkReplay({ call }: { call: CallDetail }) {
  const network = call.diagnostics?.network
  const [index, setIndex] = useState(Math.max(0, call.attempts.length - 1))
  const [playing, setPlaying] = useState(false)
  const [position, setPosition] = useState(0)
  const [speed, setSpeed] = useState(1)
  const [reduced, setReduced] = useState(() => window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false)
  const maskId = useId().replaceAll(':', '')
  const spans = useMemo(() => replaySpans(call, index), [call, index])
  const total = replayDuration(call, spans)
  useEffect(() => {
    const media = window.matchMedia?.('(prefers-reduced-motion: reduce)')
    if (!media) return
    const change = () => { setReduced(media.matches); if (media.matches) setPlaying(false) }
    media.addEventListener('change', change)
    return () => media.removeEventListener('change', change)
  }, [])
  useEffect(() => {
    if (!playing || reduced) return
    let frame = 0
    let previous: number | undefined
    const tick = (now: number) => {
      if (previous !== undefined) setPosition(p => Math.min(total, p + (now - previous!) * speed))
      previous = now
      frame = requestAnimationFrame(tick)
    }
    frame = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(frame)
  }, [playing, reduced, speed, total])
  useEffect(() => { if (position >= total) setPlaying(false) }, [position, total])
  if (!network) return <p className="muted">此记录未采集位置与链路时序。</p>
  if (!call.completed_at) return <p className="muted">请求完成后可回放链路。</p>
  const attempt = call.attempts[index]
  const upstream = network.attempts[index]?.node
  const nodes = [network.client, network.gateway, upstream]
  const titles = ['下游客户端', '本网关', '上游入口']
  const originalPoints = nodes.map((n, i) => mapPoint(n?.location, i))
  const points = originalPoints.map((point, i) => {
    const peers = originalPoints.filter(p => Math.hypot(p.x - point.x, p.y - point.y) < 22)
    return peers.length > 1 ? { x: Math.max(12, Math.min(708, point.x + (i - 1) * 36)), y: Math.max(12, Math.min(350, point.y + (i - 1) * 20)) } : point
  })
  const active = spans.filter(s => position >= s.start && position < s.end)
  const inbound = active.some(s => s.label === '读取与解析请求')
  const direction = active.some(s => s.side === 'response') ? 'response' : active.some(s => s.side === 'upstream') ? 'upstream' : inbound ? 'inbound' : 'gateway'
  const finished = position >= total
  return <div className="network-replay">
    <div className="network-toolbar">
      {call.attempts.length > 0 && <label>尝试 <select aria-label="选择上游尝试" value={index} onChange={e => { setIndex(Number(e.target.value)); setPosition(0); setPlaying(false) }}>
        {call.attempts.map((a, i) => <option value={i} key={i}>#{i + 1} · {a.channel?.name ?? '未知渠道'}</option>)}
      </select></label>}
      <OutcomeBadge outcome={call.outcome} />
      {attempt && <span className="muted">本次：{endReasonLabel(attempt.end_reason)}{attempt.status_code ? ` · ${attempt.status_code}` : ''}</span>}
      {attempt?.reused_connection != null && <span className="muted">{attempt.reused_connection ? '连接复用' : '新建连接'}</span>}
    </div>
    <div className={`network-map ${playing ? 'is-playing' : ''} direction-${direction}`}>
      <svg viewBox="0 0 720 406" role="img" aria-label="客户端、本网关与上游入口的近似位置；连线仅表示流向">
        <defs><mask id={maskId}><image href="/maps/world.svg" width="720" height="360" filter="invert(1)" /></mask></defs>
        <rect width="720" height="360" className="network-land" mask={`url(#${maskId})`} />
        {[90, 180, 270].map(y => <path key={y} d={`M0 ${y}H720`} className="network-grid" />)}
        {[180, 360, 540].map(x => <path key={x} d={`M${x} 0V360`} className="network-grid" />)}
        {[0, 1].map(i => {
          const a = points[i], b = points[i + 1]
          const known = nodes[i]?.location && nodes[i + 1]?.location
          // A curved schematic link, not the network's physical route.
          const d = `M${a.x} ${a.y} Q${(a.x + b.x) / 2} ${Math.max(8, Math.min(a.y, b.y) - 40)} ${b.x} ${b.y}`
          return <g key={i} className={known ? '' : 'network-unknown'}>
            <path d={d} className="network-link" />
            <path d={d} className={`network-flow ${direction === 'response' || direction === 'upstream' && i === 1 || direction === 'inbound' && i === 0 ? 'is-active' : ''}`} />
          </g>
        })}
        {points.map((p, i) => <g key={i} transform={`translate(${Math.max(12, Math.min(708, p.x))} ${Math.max(12, p.y)})`}>
          <path d={`M${originalPoints[i].x - p.x} ${originalPoints[i].y - p.y}L0 0`} className="network-link" />
          <circle r={i === 1 ? 12 : 9} className={`network-node node-${i}`} />
          <text dominantBaseline="central" textAnchor="middle" className="network-node-number">{i + 1}</text>
        </g>)}
      </svg>
    </div>
    <ol className="network-nodes">
      {nodes.map((node, i) => <li key={i}>
        <strong>{i + 1} · {titles[i]}</strong>
        <span>{node?.location?.label ?? (i === 2 && !node?.ip ? '未建立连接' : '位置未知')}</span>
        {node?.ip && <code>{node.ip}</code>}
        {i === 0 && <small className="muted">到网关的网络耗时未测量</small>}
        {i === 1 && <small className="muted">{formatMs(call.duration_ms)} · 网关侧总耗时</small>}
        {i === 2 && attempt && <small className="muted">首响应 {formatMs(attempt.ttft_ms)} · 本次 {formatMs(attempt.duration_ms)}</small>}
      </li>)}
    </ol>
    <div className="network-controls">
      <Button type="button" variant="secondary" disabled={reduced || spans.length === 0} onClick={() => { if (finished) setPosition(0); setPlaying(!playing) }}>{playing ? '暂停' : finished ? '重播' : '播放'}</Button>
      <Button type="button" variant="quiet" onClick={() => { setPosition(0); setPlaying(false) }}>回到起点</Button>
      <label>速度 <select aria-label="回放速度" value={speed} onChange={e => setSpeed(Number(e.target.value))}>
        {[1, 5, 20, 100].map(n => <option key={n} value={n}>{n}×</option>)}
      </select></label>
      <output className="num">{formatMs(position)} / {formatMs(total)}</output>
    </div>
    <input className="network-seek" type="range" aria-label="回放进度" min="0" max={total} step="1" value={position} onChange={e => { setPosition(Number(e.target.value)); setPlaying(false) }} />
    <p className="network-current" role="status">{finished ? '回放结束' : active.map(s => s.label).join(' · ') || '等待阶段开始'}{reduced && ' · 已减少动态效果，可拖动进度查看'}</p>
    <ol className="network-timeline">
      {spans.map((span, i) => <li key={i} className={active.includes(span) ? 'is-current' : ''}>
        <div><span>{span.label}</span><span className="num">{formatMs(span.end - span.start)}</span></div>
        <svg viewBox="0 0 1000 12" preserveAspectRatio="none" role="img" aria-label={`从 ${formatMs(span.start)} 到 ${formatMs(span.end)}`}>
          <rect width="1000" height="12" className="network-track" />
          <rect x={span.start / total * 1000} width={Math.max(1, (span.end - span.start) / total * 1000)} height="12" className={`network-span side-${span.side}`} />
          <path d={`M${position / total * 1000} 0v12`} className="network-cursor" />
        </svg>
      </li>)}
    </ol>
    <small className="muted">位置为近似地区，上游点表示连接入口。连线仅示意流向，同地区节点会错开显示；阶段可能重叠，不能直接相加。{network.location_source && <> 定位：<a href="https://ipwhois.io/" target="_blank" rel="noreferrer">{network.location_source}</a>。</>} 底图：<a href="/licenses/map.txt" target="_blank" rel="noreferrer">Natural Earth</a>。</small>
  </div>
}
