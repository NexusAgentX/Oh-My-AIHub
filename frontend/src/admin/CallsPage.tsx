import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  CallExplorer,
  CallFilterBar,
  emptyCallFilters,
  filtersToParams,
  summaryColumns,
  userCallsPath,
  type CallColumn,
  type CallFilterState,
} from '../calls'
import { Card, PageHeader } from '../ui'
import { withQuery } from './api'
import { useAdminAccounts, useAdminChannels, useAdminModels } from './queries'
import type { AdminCall } from './types'

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
  const [search, setSearch] = useSearchParams()
  const [filters, setFilters] = useState<CallFilterState>(emptyCallFilters)
  const accountID = search.get('account_id') ?? ''
  const channelID = search.get('channel_id') ?? ''
  const openCall = search.get('call')
  const accounts = useAdminAccounts({})
  const channels = useAdminChannels({})
  const models = useAdminModels()
  const params = useMemo(
    () => ({ ...filtersToParams(filters), account_id: accountID || undefined, channel_id: channelID || undefined }),
    [filters, accountID, channelID],
  )
  const setParam = (key: string, value: string | null) => {
    const next = new URLSearchParams(search)
    if (value) next.set(key, value)
    else next.delete(key)
    setSearch(next, { replace: true })
  }
  const withSelected = (options: Array<{ value: string; label: string }>, selected: string) =>
    selected && !options.some((option) => option.value === selected)
      ? [{ value: selected, label: selected.slice(0, 8) }, ...options]
      : options

  return (
    <>
      <PageHeader title="调用" />
      <Card>
        <CallExplorer<AdminCall>
          caption="全部调用"
          columns={adminColumns()}
          detailPath={userCallsPath}
          listPath={adminCallsPath}
          onOpenCall={(id) => setParam('call', id)}
          openCallId={openCall}
          params={params}
          streamPath={adminStreamPath(accountID, channelID)}
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
                  channels.data?.map((channel) => ({ value: channel.id, label: channel.name })) ?? [],
                  channelID,
                )}
                value={channelID}
              />
              <CallFilterBar
                models={models.data?.items.map((model) => model.id)}
                onChange={setFilters}
                onSearchId={(id) => setParam('call', id)}
                value={filters}
              />
            </div>
          }
        />
      </Card>
    </>
  )
}
