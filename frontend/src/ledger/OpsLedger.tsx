import { Card, QueryBoundary } from '../ui'
import { LedgerEntriesTable } from '../wallet/LedgerEntriesTable'
import { formatPointAmount } from '../wallet/presentation'
import { FeeRatePanel } from './FeeRatePanel'
import { useOpsMetricsQuery, useSystemAccountQuery, type SystemKind } from './queries'

function SystemAccountCard({ kind, label }: { kind: SystemKind; label: string }) {
  const query = useSystemAccountQuery(kind)
  return (
    <QueryBoundary errorFallback={`${label}加载失败`} query={query}>
      {({ wallet, entries }) => (
        <Card
          actions={<strong className="num">{formatPointAmount(wallet.posted_balance)} 积分</strong>}
          flush
          title={label}
        >
          <LedgerEntriesTable entries={entries} loadingMore={false} nextBefore="" onLoadMore={() => undefined} />
        </Card>
      )}
    </QueryBoundary>
  )
}

/** 一致性校验、持有中积分、平台系统账户与全局手续费率。 */
export function OpsLedger() {
  const query = useOpsMetricsQuery(24)
  return (
    <div className="ops-stack">
      <QueryBoundary errorFallback="一致性指标加载失败" query={query}>
        {({ ledger }) => (
          <div className="ops-grid">
            <Card title="一致性校验">
              <dl className="ops-list">
                <div><dt>余额投影差额</dt><dd className="num">{formatPointAmount(ledger.posted_projection_difference)}</dd></div>
                <div><dt>余额投影异常账户</dt><dd className="num">{ledger.posted_projection_mismatch_accounts}</dd></div>
                <div><dt>资产冻结投影差额</dt><dd className="num">{formatPointAmount(ledger.asset_reservation_difference)}</dd></div>
                <div><dt>消费授权投影差额</dt><dd className="num">{formatPointAmount(ledger.spend_authorization_difference)}</dd></div>
                <div><dt>持有投影异常账户</dt><dd className="num">{ledger.hold_projection_mismatch_accounts}</dd></div>
              </dl>
            </Card>
            <Card title="持有中的积分">
              <dl className="ops-list">
                <div><dt>资产冻结</dt><dd className="num">{formatPointAmount(ledger.asset_reserved)}</dd></div>
                <div><dt>消费授权</dt><dd className="num">{formatPointAmount(ledger.spend_authorized)}</dd></div>
                <div><dt>账本账户</dt><dd className="num">{ledger.ledger_account_count}</dd></div>
              </dl>
            </Card>
          </div>
        )}
      </QueryBoundary>
      <FeeRatePanel />
      <SystemAccountCard kind="platform_incentive" label="平台激励账户" />
      <SystemAccountCard kind="platform_loss" label="平台损失账户" />
    </div>
  )
}

