import { Card, Notice } from '../ui'
import { formatTypicalAmount, formatTypicalUnit } from './format'
import type { TypicalEstimate } from './queries'
const reasons: Record<string, string> = { no_samples: '无可靠样本', missing_reliable_conversion: '缺少用量换算依据', unpriced_workload_component: '工作负载有未计价分量', no_reference: '无原厂参考价', zero_reference: '原厂参考价为零', reference_workload_unpriced: '参考价未覆盖全部用量', estimate_overflow: '费用超过估算范围' }
const sources: Record<string, string> = { group_current_quote: '本分组当前报价样本', model_site_fallback: '站内同模型结构推算', site_active_groups_weighted: '站内有效分组加权', site_no_samples: '站内暂无样本' }
export function EstimateCard({ item }: { item: TypicalEstimate }) {
  const name = item.level === 'channel' ? item.channel_name : item.level === 'group' ? item.group_name : '站内整体'
  return <Card title={`${name || (item.level === 'channel' ? '渠道' : '分组')} · ${item.model_id}`}>
    {item.supplier_name && <p className="muted-copy">{item.supplier_name}</p>}
    <dl className="detail-list compact-list">
      <div><dt>典型费用 / {formatTypicalUnit(item.unit, item.quantity)}</dt><dd>{formatTypicalAmount(item.cost)}</dd></div>
      <div><dt>手续费（另计）</dt><dd>{formatTypicalAmount(item.fee)}</dd></div>
      <div><dt>原厂参考倍率</dt><dd>{item.ratio === null ? '未知' : `${Number(item.ratio).toLocaleString(undefined, { maximumFractionDigits: 4 })}×`}</dd></div>
      <div><dt>{item.request_quantity.toLocaleString()} 次请求估算</dt><dd>{formatTypicalAmount(item.request_cost)}</dd></div>
      <div><dt>样本 / 当前分组</dt><dd>{item.samples} / {item.own_samples}</dd></div>
      <div><dt>缓存读占提示侧 token</dt><dd>{item.cache_read_share === null ? '未知' : `${(Number(item.cache_read_share) * 100).toLocaleString(undefined, { maximumFractionDigits: 2 })}%`}</dd></div>
    </dl>
    <p className="muted-copy">{sources[item.source] || item.source}{item.quote_version ? ` · 报价版本 ${item.quote_version}` : ''}</p>
    {item.reason && <Notice tone={item.cost === null ? 'warning' : 'info'}>{reasons[item.reason] || '暂时无法估算'}</Notice>}
  </Card>
}
