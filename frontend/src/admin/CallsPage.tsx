import {
  CallExplorer,
  CallFilterBar,
  summaryColumns,
  userCallsPath,
  type CallColumn,
} from '../calls'
import { Card, PageHeader } from '../ui'
import { withQuery } from './api'
import { useAdminAccounts } from './queries'
import { useGroupChannels } from '../group-channels/queries'
import { useMarketNames } from '../market/queries'
import type { AdminCall } from './types'
import { CallSectionNav } from '../calls/CallSectionNav'
import { useCallFilters } from '../calls/urlFilters'
import { useQueryOverlay } from '../ui/urlState'
import { CallFacet } from '../calls/CallFacet'

export const adminCallsPath = '/api/admin/calls'
export const adminCallsStreamPath = '/api/admin/calls/stream'

/**
 * 实时流只按服务端支持的筛选（模型、Key、渠道、结果）推送；按用户筛选时关闭实时，
 * 避免把其他用户的新调用插进列表。
 */
export function adminStreamPath(accountID: string, channelID: string) {
  if (accountID) return undefined
  return withQuery(adminCallsStreamPath, { channel_id: channelID || undefined })
}

function adminColumns(): CallColumn<AdminCall>[] {
  const [time, ...rest] = summaryColumns<AdminCall>({ showFormat: false })
  return [
    time,
    { key: 'user', header: '用户', cell: (row) => row.account.display_name },
    ...rest,
  ]
}

function FilterSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  options: Array<{ value: string; label: string }>
}) {
  return (
    <label className="filter-select">
      <span className="visually-hidden">{label}</span>
      <select aria-label={label} className="input select-input" onChange={(event) => onChange(event.target.value)} value={value}>
        <option value="">{label}：全部</option>
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
    </label>
  )
}

export function CallsPage() {
  const { filters, setFilters, params, url, update, clear } = useCallFilters()
  const accountID = url.account_id
  const channelID = filters.channel_id ?? ''
  const [openCall, setOpenCall] = useQueryOverlay('call')
  const accounts = useAdminAccounts({})
  const channels = useGroupChannels(true)
  const models = useMarketNames()
  const setParam = (key: string, value: string | null) => {
    update({ [key]: value ?? '' })
  }
  const withSelected = (options: Array<{ value: string; label: string }>, selected: string) =>
    selected && !options.some((option) => option.value === selected)
      ? [{ value: selected, label: selected.slice(0, 8) }, ...options]
      : options

  return (
    <>
      <PageHeader title="调用" />
      <CallSectionNav admin />
      <CallFacet params={params} onClear={() => update({ dimension: '', dimension_value: '', second_dimension: '', second_value: '' })} />
      <Card>
        <CallExplorer<AdminCall>
          caption="全部调用"
          columns={adminColumns()}
          detailPath={userCallsPath}
          listPath={adminCallsPath}
          onOpenCall={setOpenCall}
          openCallId={openCall}
          params={params}
          streamPath={adminStreamPath(accountID, channelID)}
          liveDisabledReason={filters.to ? '固定结束时间不支持实时' : filters.requestId ? '精确查找不支持实时' : undefined}
          toolbar={
            <div className="admin-call-filters">
              <FilterSelect
                label="用户"
                onChange={(value) => setParam('account_id', value)}
                options={withSelected(
                  accounts.data?.map((account) => ({ value: account.id, label: account.display_name })) ?? [],
                  accountID,
                )}
                value={accountID}
              />
              <FilterSelect
                label="渠道"
                onChange={(value) => setParam('channel_id', value)}
                options={withSelected(
                  channels.data?.items.map((channel) => ({ value: channel.id, label: channel.name })) ?? [],
                  channelID,
                )}
                value={channelID}
              />
              <CallFilterBar onClear={clear}
                models={models.data?.items.map((model) => model.id)}
                onChange={setFilters}
                value={filters}
              />
            </div>
          }
        />
      </Card>
    </>
  )
}
