import { NavLink, Link } from 'react-router-dom'
import type { components } from '../api/schema.gen'
import { CapabilityTags } from '../channels/Capabilities'
import { FormatTags } from '../calls'
import { formatMs, formatRatio } from '../money/format'
import { Badge, Card } from '../ui'
import './sharing.css'

export type Supplier = components['schemas']['Supplier']
export type HallChannel = components['schemas']['HallChannel']
export type CatalogModel = components['schemas']['CatalogModel']

export function SharingNav() {
  return <nav className="sharing-nav" aria-label="共享栏目">
    <NavLink to="/channel-hall">渠道大厅</NavLink><NavLink to="/suppliers">供应商</NavLink><NavLink to="/channels">我的渠道</NavLink>
  </nav>
}
export function SupplierIcon({ supplier }: { supplier?: Pick<Supplier, 'name' | 'icon_data'> | null }) {
  return supplier?.icon_data ? <img className="supplier-icon" src={supplier.icon_data} alt="" width={44} height={44} /> : <span aria-hidden="true" className="supplier-icon supplier-initial">{supplier?.name.slice(0, 1) || '渠'}</span>
}
export function SupplierCard({ supplier }: { supplier: Supplier }) {
  return <Card className="supplier-card"><div className="sharing-title"><SupplierIcon supplier={supplier} /><div><Link className="sharing-name" to={`/suppliers/${supplier.id}`}>{supplier.name}</Link><div><Badge tone="info">{supplier.kind === 'official' ? '官方 API' : '中转站'}</Badge></div></div></div>
    {supplier.summary && <p>{supplier.summary}</p>}<Link to={`/suppliers/${supplier.id}`}>查看供应商 →</Link>
  </Card>
}
export function ChannelState({ channel }: { channel: HallChannel }) {
  return <Badge tone={channel.state === 'available' ? 'success' : 'warning'}>{channel.state === 'cooldown' ? `冷却中 · ${channel.cooldown_remaining_seconds ?? 0} 秒` : channel.state === 'limited' ? '已达限额' : '可用'}</Badge>
}
export function ChannelMetrics({ channel }: { channel: HallChannel }) {
  return <dl className="sharing-metrics"><div><dt>模型</dt><dd className="num">{channel.models.length}</dd></div><div><dt>24h 成功率</dt><dd className="num">{channel.success_rate_24h === null ? '暂无数据' : formatRatio(channel.success_rate_24h)}</dd></div><div><dt>首字时间</dt><dd className="num">{channel.ttft_p50_ms === null ? '暂无数据' : formatMs(channel.ttft_p50_ms)}</dd></div></dl>
}
export function ChannelCard({ channel }: { channel: HallChannel }) {
  return <Card className="hall-card"><div className="sharing-title"><SupplierIcon supplier={channel.supplier} /><div><Link className="sharing-name" to={`/channel-hall/${channel.id}`}>{channel.name}</Link><div className="muted-copy">由 {channel.owner.display_name} 提供</div></div></div>
    <div className="sharing-line"><ChannelState channel={channel} />{channel.supplier ? <Link to={`/suppliers/${channel.supplier.id}`}>{channel.supplier.name}</Link> : <span className="muted-copy">未识别供应商</span>}</div>
    {channel.summary && <p>{channel.summary}</p>}<ChannelMetrics channel={channel} /><FormatTags formats={channel.formats} /><CapabilityTags values={[...new Set([...(channel.capabilities ?? []), ...channel.models.flatMap(m => m.capabilities ?? [])])]} />
  </Card>
}
