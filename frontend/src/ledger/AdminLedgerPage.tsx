import { useEffect, useState } from 'react'
import { api, ApiError } from '../api/client'
import type { LedgerEntry, LedgerMetrics, Wallet } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { InlineError, LoadingState } from '../ui/FormControls'
import { LedgerEntriesTable } from '../wallet/LedgerEntriesTable'
import { formatPointAmount } from '../wallet/presentation'

type SystemAccountView = {
  wallet: Wallet
  entries: LedgerEntry[]
}

export function AdminLedgerPage() {
  const [metrics, setMetrics] = useState<LedgerMetrics | null>(null)
  const [incentive, setIncentive] = useState<SystemAccountView | null>(null)
  const [loss, setLoss] = useState<SystemAccountView | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    void Promise.all([
      api.ledgerMetrics(),
      Promise.all([api.adminSystemWallet('platform_incentive'), api.adminSystemEntries('platform_incentive', '', 5)]),
      Promise.all([api.adminSystemWallet('platform_loss'), api.adminSystemEntries('platform_loss', '', 5)]),
    ])
      .then(([nextMetrics, [incentiveWallet, incentiveEntries], [lossWallet, lossEntries]]) => {
        setMetrics(nextMetrics)
        setIncentive({ wallet: incentiveWallet, entries: incentiveEntries.entries })
        setLoss({ wallet: lossWallet, entries: lossEntries.entries })
      })
      .catch((caught) => {
        setError(caught instanceof ApiError ? caught.message : '运营指标加载失败')
      })
      .finally(() => setLoading(false))
  }, [])

  return (
    <AppShell admin>
      <header className="page-heading">
        <div><h1>运营总览</h1></div>
        {metrics && (
          <span className={`ledger-health ${metrics.ledger_consistent ? 'ledger-health-ok' : 'ledger-health-error'}`}>
            {metrics.ledger_consistent ? '账本已核对' : '账本异常'}
          </span>
        )}
      </header>
      <InlineError>{error}</InlineError>
      {loading ? <LoadingState /> : metrics ? (
        <>
          <section className="metric-grid" aria-label="账本核心指标">
            <article className="metric-card metric-card-accent">
              <span>全账户余额和</span>
              <strong>{formatPointAmount(metrics.total_posted_balance)}</strong>
              <small>{metrics.zero_sum ? '零和正常' : '需要处理'}</small>
            </article>
            <article className="metric-card">
              <span>正 / 负余额</span>
              <strong>{formatPointAmount(metrics.positive_posted_balance)}</strong>
              <small>{formatPointAmount(metrics.negative_posted_balance)}</small>
            </article>
            <article className="metric-card metric-card-warm">
              <span>总信用额度</span>
              <strong>{formatPointAmount(metrics.total_credit_limit)}</strong>
              <small>已用 {formatPointAmount(metrics.credit_capacity_used)}</small>
            </article>
            <article className="metric-card">
              <span>信用风险账户</span>
              <strong>{metrics.over_limit_accounts}</strong>
              <small>{metrics.credit_frozen_accounts} 个信用冻结</small>
            </article>
          </section>

          <section className="ops-grid">
            <article className="panel projection-panel">
              <header className="panel-heading"><h2>一致性校验</h2></header>
              <dl className="ops-list">
                <div><dt>余额投影差额</dt><dd>{formatPointAmount(metrics.posted_projection_difference)}</dd></div>
                <div><dt>余额投影异常账户</dt><dd>{metrics.posted_projection_mismatch_accounts}</dd></div>
                <div><dt>资产冻结投影差额</dt><dd>{formatPointAmount(metrics.asset_reservation_difference)}</dd></div>
                <div><dt>消费授权投影差额</dt><dd>{formatPointAmount(metrics.spend_authorization_difference)}</dd></div>
                <div><dt>持有投影异常账户</dt><dd>{metrics.hold_projection_mismatch_accounts}</dd></div>
              </dl>
            </article>
            <article className="panel projection-panel">
              <header className="panel-heading"><h2>持有中的积分</h2></header>
              <dl className="ops-list">
                <div><dt>资产冻结</dt><dd>{formatPointAmount(metrics.asset_reserved)}</dd></div>
                <div><dt>消费授权</dt><dd>{formatPointAmount(metrics.spend_authorized)}</dd></div>
                <div><dt>账本账户</dt><dd>{metrics.ledger_account_count}</dd></div>
              </dl>
            </article>
          </section>

          <SystemAccountPanel label="平台激励账户" view={incentive} />
          <SystemAccountPanel label="平台损失账户" view={loss} />
        </>
      ) : null}
    </AppShell>
  )
}

function SystemAccountPanel({ label, view }: { label: string; view: SystemAccountView | null }) {
  if (!view) return null
  return (
    <section className="panel table-panel system-ledger-panel">
      <header className="table-toolbar">
        <h2>{label}</h2>
        <strong>{formatPointAmount(view.wallet.posted_balance)} 积分</strong>
      </header>
      <LedgerEntriesTable
        entries={view.entries}
        loadingMore={false}
        nextBefore=""
        onLoadMore={() => undefined}
      />
    </section>
  )
}
