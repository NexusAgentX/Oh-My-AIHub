import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { CallSummary } from '../api/types'
import {
  CallExplorer,
  CallFilterBar,
  emptyCallFilters,
  filtersToParams,
  summaryColumns,
  userCallsPath,
  userCallsStreamPath,
  type CallFilterState,
} from '../calls'
import { useKeys } from '../keys/queries'
import { useMarketNames } from '../market/queries'
import { Card, PageHeader } from '../ui'

export function UsagePage() {
  const [search, setSearch] = useSearchParams()
  const [filters, setFilters] = useState<CallFilterState>(emptyCallFilters)
  const keys = useKeys()
  const models = useMarketNames()
  const params = useMemo(() => filtersToParams(filters), [filters])
  const openCall = search.get('call')
  const setOpenCall = (id: string | null) => {
    const next = new URLSearchParams(search)
    if (id) next.set('call', id)
    else next.delete('call')
    setSearch(next, { replace: true })
  }
  return (
    <>
      <PageHeader title="用量" />
      <Card title="调用">
        <CallExplorer<CallSummary>
          caption="调用记录"
          columns={summaryColumns()}
          detailPath={userCallsPath}
          listPath={userCallsPath}
          onOpenCall={setOpenCall}
          openCallId={openCall}
          params={params}
          streamPath={userCallsStreamPath}
          toolbar={
            <CallFilterBar
              keys={keys.data?.items.map((key) => ({ id: key.id, name: key.name }))}
              models={models.data?.items.map((model) => model.id)}
              onChange={setFilters}
              value={filters}
            />
          }
        />
      </Card>
    </>
  )
}
