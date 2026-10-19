import type { ChannelCall } from '../api/types'
import { CallExplorer, channelCallColumns, outcomeLabels, outcomeOptions } from '../calls'
import { rangeFrom } from '../money/time'
import { Card, Segmented } from '../ui'
import { CommittedInput } from '../ui/CommittedInput'
import { useShanghaiDay } from '../ui/clock'
import { enumField, stringField, useQueryOverlay, useUrlState } from '../ui/urlState'

const schema = { range: enumField(['today', '7d', '30d', 'all'] as const, 'today'), outcome: stringField(), model: stringField(), from: stringField(), to: stringField() }
export function ChannelCalls({ channelId }: { channelId: string }) {
  useShanghaiDay()
  const [filters, update] = useUrlState(schema), [call, setCall] = useQueryOverlay('call')
  const params = { from: filters.from || rangeFrom(filters.range), to: filters.to || undefined, outcome: filters.outcome || undefined, model: filters.model || undefined }
  return <Card title="调用"><CallExplorer<ChannelCall> caption="渠道调用" chargedLabel="收入" columns={channelCallColumns()} listPath={`/api/channels/${channelId}/calls`} streamPath={`/api/channels/${channelId}/calls/stream`} detailPath="/api/calls" params={params} openCallId={call} onOpenCall={setCall} liveDisabledReason={filters.to ? '固定结束时间的记录不支持实时' : undefined} toolbar={<div className="call-filters"><Segmented label="时间范围" value={filters.range} onChange={range => update({ range, from: '', to: '' })} options={[{ key: 'today', label: '今天' }, { key: '7d', label: '7 天' }, { key: '30d', label: '30 天' }, { key: 'all', label: '全部' }]} /><select aria-label="结果" className="input select-input" value={filters.outcome} onChange={event => update({ outcome: event.target.value })}><option value="">结果：全部</option>{outcomeOptions.map(item => <option key={item} value={item}>{outcomeLabels[item]}</option>)}</select><CommittedInput aria-label="模型" placeholder="模型 ID" className="input" value={filters.model} onCommit={model => update({ model })} /></div>} /></Card>
}
