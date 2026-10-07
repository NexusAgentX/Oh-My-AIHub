import { useSearchParams } from 'react-router-dom'
import { errorMessage } from '../api/query'
import {
  Badge,
  Button,
  Icon,
  InlineError,
  PageHeader,
  Segmented,
  Tabs,
  Toolbar,
} from '../ui'
import { OpsAnomalies } from './OpsAnomalies'
import { OpsEvidence } from './OpsEvidence'
import { OpsLedger } from './OpsLedger'
import { OpsOverview } from './OpsOverview'
import { OpsProviders } from './OpsProviders'
import {
  isWindowedTab,
  opsTabs,
  parseTab,
  parseWindow,
  windowHours,
  windowOptions,
  type OpsTab,
  type WindowKey,
} from './opsFormat'
import { useOpsMetricsQuery, useRunInspection } from './queries'

/** 运营台：时间窗口、硬异常与固定下钻常驻；总览、共享者收入、试用与巡检、账本与费率分区切换。 */
export function AdminLedgerPage() {
  const [params, setParams] = useSearchParams()
  const tab = parseTab(params.get('tab'))
  const windowKey = parseWindow(params.get('window'))
  const hours = windowHours(windowKey)
  const metrics = useOpsMetricsQuery(hours)
  const inspection = useRunInspection()

  const update = (patch: { tab?: OpsTab; window?: WindowKey }) => {
    const next = new URLSearchParams(params)
    if (patch.tab) next.set('tab', patch.tab)
    if (patch.window) next.set('window', patch.window)
    setParams(next, { replace: true })
  }

  const consistent = metrics.data?.ledger.ledger_consistent
  return (
    <>
      <PageHeader
        actions={
          <>
            {consistent !== undefined && (
              <Badge tone={consistent ? 'success' : 'danger'}>
                {consistent ? '账本已核对' : '账本异常'}
              </Badge>
            )}
            <Button
              icon={<Icon name="refresh" />}
              loading={inspection.isPending}
              onClick={() => inspection.mutate()}
              variant="secondary"
            >
              立即巡检
            </Button>
          </>
        }
        title="运营台"
      />
      <InlineError>
        {inspection.isError ? errorMessage(inspection.error, '巡检执行失败') : ''}
      </InlineError>
      <OpsAnomalies />
      {isWindowedTab(tab) && (
        <Toolbar>
          <Segmented
            label="指标时间窗口"
            onChange={(key) => update({ window: key })}
            options={windowOptions}
            value={windowKey}
          />
        </Toolbar>
      )}
      <Tabs
        items={opsTabs}
        label="运营台分区"
        onChange={(key) => update({ tab: key })}
        value={tab}
      >
        {tab === 'overview' && <OpsOverview hours={hours} />}
        {tab === 'providers' && <OpsProviders hours={hours} />}
        {tab === 'evidence' && <OpsEvidence />}
        {tab === 'ledger' && <OpsLedger />}
      </Tabs>
    </>
  )
}
