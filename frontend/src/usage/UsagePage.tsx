import { useLocation } from 'react-router-dom'
import type { CallSummary } from '../api/types'
import { CallExplorer, CallFilterBar, summaryColumns, userCallsPath, userCallsStreamPath } from '../calls'
import { CallSectionNav } from '../calls/CallSectionNav'
import { useCallFilters } from '../calls/urlFilters'
import { CallFacet } from '../calls/CallFacet'
import { useKeys } from '../keys/queries'
import { useMarketNames } from '../market/queries'
import { Card, PageHeader } from '../ui'
import { useBack, useQueryOverlay } from '../ui/urlState'

export function UsagePage() {
  const { filters, setFilters, params, clear, update } = useCallFilters()
  const [openCall, setOpenCall] = useQueryOverlay('call')
  const location = useLocation()
  const back = useBack('/home', '首页')
  const keys = useKeys()
  const models = useMarketNames()
  return <>
    <PageHeader title="调用" back={location.state?.from ? back : undefined} />
    <CallSectionNav />
    <CallFacet params={params} onClear={() => update({ dimension: '', dimension_value: '', second_dimension: '', second_value: '' })} />
    <Card>
      <CallExplorer<CallSummary> caption="调用记录" columns={summaryColumns()} detailPath={userCallsPath} listPath={userCallsPath}
        onOpenCall={setOpenCall} openCallId={openCall} params={params} streamPath={userCallsStreamPath}
        liveDisabledReason={filters.to ? '固定结束时间不支持实时' : filters.requestId ? '精确查找不支持实时' : undefined}
        toolbar={<CallFilterBar onClear={clear} keys={keys.data?.items.map(key => ({ id: key.id, name: key.name }))}
          models={models.data?.items.map(model => model.id)} onChange={setFilters} value={filters} />} />
    </Card>
  </>
}
