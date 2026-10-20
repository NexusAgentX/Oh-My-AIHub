import { useState } from 'react'
import type { ChannelCall } from '../api/types'
import { CallExplorer, channelCallColumns, outcomeLabels, outcomeOptions } from '../calls'
import { rangeFrom, type RangePreset } from '../money/time'
import { Card, Segmented } from '../ui'

export function ChannelCalls({ channelId }: { channelId: string }) {
  const [range, setRange] = useState<RangePreset>('today')
  const [outcome, setOutcome] = useState('')
  const params = { from: rangeFrom(range), outcome: outcome || undefined }
  return (
    <Card title="调用">
      <CallExplorer<ChannelCall>
        key={JSON.stringify(params)}
        pageSize={20}
        caption="渠道调用"
        chargedLabel="收入"
        columns={channelCallColumns()}
        listPath={`/api/channels/${channelId}/calls`}
        params={params}
        streamPath={`/api/channels/${channelId}/calls/stream`}
        toolbar={
          <div className="call-filters">
            <Segmented
              label="时间范围"
              onChange={setRange}
              options={[
                { key: 'today', label: '今天' },
                { key: '7d', label: '7 天' },
                { key: '30d', label: '30 天' },
              ]}
              value={range}
            />
            <label className="filter-select">
              <span className="visually-hidden">结果</span>
              <select aria-label="结果" className="input select-input" onChange={(event) => setOutcome(event.target.value)} value={outcome}>
                <option value="">结果：全部</option>
                {outcomeOptions.map((item) => (
                  <option key={item} value={item}>
                    {outcomeLabels[item]}
                  </option>
                ))}
              </select>
            </label>
          </div>
        }
      />
    </Card>
  )
}
