import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type {
  LedgerEntry,
  OpsAnomalies,
  OpsInspection,
  OpsMetrics,
  OpsTrialSummary,
  Wallet,
} from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { InlineError, LoadingState } from '../ui/FormControls'
import { LedgerEntriesTable } from '../wallet/LedgerEntriesTable'
import { formatPointAmount } from '../wallet/presentation'

type SystemAccountView = {
  wallet: Wallet
  entries: LedgerEntry[]
}

type WindowPreset = { label: string; hours: number }

const windowPresets: WindowPreset[] = [
  { label: '过去 24 小时', hours: 24 },
  { label: '过去 7 天', hours: 24 * 7 },
  { label: '过去 30 天', hours: 24 * 30 },
]

function windowBounds(hours: number) {
  const to = new Date()
  const from = new Date(to.getTime() - hours * 3600 * 1000)
  return { from: from.toISOString(), to: to.toISOString() }
}

function formatFen(fen: number | null) {
  if (fen === null) return '—'
  return `¥${(fen / 100).toFixed(2)}`
}

function formatShare(value: string | null) {
  if (value === null) return '—'
  return `${(Number(value) * 100).toFixed(2)}%`
}

export function AdminLedgerPage() {
  const [presetIndex, setPresetIndex] = useState(0)
  const [metrics, setMetrics] = useState<OpsMetrics | null>(null)
  const [anomalies, setAnomalies] = useState<OpsAnomalies | null>(null)
  const [inspections, setInspections] = useState<OpsInspection[]>([])
  const [trial, setTrial] = useState<OpsTrialSummary | null>(null)
  const [incentive, setIncentive] = useState<SystemAccountView | null>(null)
  const [loss, setLoss] = useState<SystemAccountView | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async (hours: number) => {
    const bounds = windowBounds(hours)
    try {
      const [nextMetrics, nextAnomalies, nextInspections, nextTrial, incentiveView, lossView] = await Promise.all([
        api.opsMetrics(bounds.from, bounds.to),
        api.opsAnomalies(),
        api.opsInspections(10),
        api.opsTrialSummary(),
        Promise.all([api.adminSystemWallet('platform_incentive'), api.adminSystemEntries('platform_incentive', '', 5)]),
        Promise.all([api.adminSystemWallet('platform_loss'), api.adminSystemEntries('platform_loss', '', 5)]),
      ])
      setMetrics(nextMetrics)
      setAnomalies(nextAnomalies)
      setInspections(nextInspections)
      setTrial(nextTrial)
      setIncentive({ wallet: incentiveView[0], entries: incentiveView[1].entries })
      setLoss({ wallet: lossView[0], entries: lossView[1].entries })
      setError('')
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '运营指标加载失败')
    }
  }, [])

  useEffect(() => {
    setLoading(true)
    void load(windowPresets[0].hours).finally(() => setLoading(false))
  }, [load])

  const applyPreset = async (index: number) => {
    setPresetIndex(index)
    setRefreshing(true)
    await load(windowPresets[index].hours)
    setRefreshing(false)
  }

  const runInspection = async () => {
    setRefreshing(true)
    try {
      await api.runOpsInspection()
      await load(windowPresets[presetIndex].hours)
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '巡检执行失败')
    }
    setRefreshing(false)
  }

  const apiFunnel = metrics?.api
  const successRateText = useMemo(() => {
    if (!apiFunnel || apiFunnel.success_rate === null) return '空样本'
    return `${(Number(apiFunnel.success_rate) * 100).toFixed(2)}%`
  }, [apiFunnel])

  return (
    <AppShell admin>
      <header className="page-heading">
        <div><h1>运营总览</h1></div>
        {metrics && (
          <span className={`ledger-health ${metrics.ledger.ledger_consistent ? 'ledger-health-ok' : 'ledger-health-error'}`}>
            {metrics.ledger.ledger_consistent ? '账本已核对' : '账本异常'}
          </span>
        )}
      </header>
      <InlineError>{error}</InlineError>
      {loading ? <LoadingState /> : metrics ? (
        <>
          <section className="table-toolbar" aria-label="时间窗口">
            <div className="segmented" role="group" aria-label="指标时间窗口">
              {windowPresets.map((preset, index) => (
                <button
                  key={preset.label}
                  type="button"
                  className={index === presetIndex ? 'segmented-item active' : 'segmented-item'}
                  onClick={() => void applyPreset(index)}
                >
                  {preset.label}
                </button>
              ))}
            </div>
            <button type="button" className="button-secondary" onClick={() => void runInspection()} disabled={refreshing}>
              立即巡检
            </button>
          </section>

          {anomalies && (anomalies.hard_anomalies.length > 0 || anomalies.attention_items.length > 0) && (
            <section className="panel" aria-label="异常与关注">
              <header className="panel-heading">
                <h2>异常与关注</h2>
                <small>硬异常 {anomalies.hard_count} · 关注项不设阈值</small>
              </header>
              {anomalies.hard_anomalies.length > 0 && (
                <ul className="anomaly-list anomaly-hard">
                  {anomalies.hard_anomalies.map((item) => (
                    <li key={item.kind}>
                      <strong>{item.detail}</strong>
                      <span>{item.count} 项</span>
                      <Link to={item.drilldown.split('?')[0]}>下钻</Link>
                    </li>
                  ))}
                </ul>
              )}
              <ul className="anomaly-list">
                {anomalies.attention_items.map((item) => (
                  <li key={item.kind}>
                    <span>{item.detail}</span>
                    <strong>{item.count}</strong>
                    {item.drilldown.startsWith('/admin') && <Link to={item.drilldown.split('?')[0]}>查看</Link>}
                  </li>
                ))}
              </ul>
            </section>
          )}

          <section className="metric-grid" aria-label="账本核心指标">
            <article className="metric-card metric-card-accent">
              <span>全账户余额和</span>
              <strong>{formatPointAmount(metrics.ledger.total_posted_balance)}</strong>
              <small>{metrics.ledger.zero_sum ? '零和正常' : '需要处理'}</small>
            </article>
            <article className="metric-card">
              <span>正 / 负余额</span>
              <strong>{formatPointAmount(metrics.ledger.positive_posted_balance)}</strong>
              <small>{formatPointAmount(metrics.ledger.negative_posted_balance)}</small>
            </article>
            <article className="metric-card metric-card-warm">
              <span>有效信用</span>
              <strong>{formatPointAmount(metrics.effective_credit)}</strong>
              <small>额度 {formatPointAmount(metrics.ledger.total_credit_limit)} · 已用 {formatPointAmount(metrics.ledger.credit_capacity_used)}</small>
            </article>
            <article className="metric-card">
              <span>信用风险账户</span>
              <strong>{metrics.ledger.over_limit_accounts}</strong>
              <small>{metrics.ledger.credit_frozen_accounts} 个信用冻结</small>
            </article>
          </section>

          <section className="metric-grid" aria-label="API 调用窗口指标">
            <article className="metric-card">
              <span>窗口调用成功率</span>
              <strong>{successRateText}</strong>
              <small>分母为到达上游且已终态（{apiFunnel?.terminal_reached ?? 0} 次）</small>
            </article>
            <article className="metric-card">
              <span>预校验拒绝</span>
              <strong>{apiFunnel?.precheck_rejected ?? 0}</strong>
              <small>零上游、零 hold</small>
            </article>
            <article className="metric-card">
              <span>到达上游 / 成功</span>
              <strong>{apiFunnel?.reached_upstream ?? 0} / {apiFunnel?.succeeded ?? 0}</strong>
              <small>全部失败 {apiFunnel?.all_failed ?? 0} · 提交后不完整 {apiFunnel?.incomplete_after_commit ?? 0}</small>
            </article>
            <article className="metric-card">
              <span>平均 TTFT / TPS</span>
              <strong>
                {apiFunnel?.average_ttft_milliseconds !== null && apiFunnel?.average_ttft_milliseconds !== undefined
                  ? `${apiFunnel.average_ttft_milliseconds} ms`
                  : '—'}
              </strong>
              <small>
                {apiFunnel?.average_tokens_per_second !== null && apiFunnel?.average_tokens_per_second !== undefined
                  ? `${apiFunnel.average_tokens_per_second} tok/s`
                  : '空样本显示 —'}
              </small>
            </article>
          </section>

          <section className="ops-grid">
            <article className="panel projection-panel">
              <header className="panel-heading"><h2>消费与收入（窗口）</h2></header>
              <dl className="ops-list">
                <div><dt>消费支出</dt><dd>{formatPointAmount(metrics.consumption.consumer_spend)}</dd></div>
                <div><dt>共享者收入（全部）</dt><dd>{formatPointAmount(metrics.consumption.provider_income)}</dd></div>
                <div><dt>来自其他消费者</dt><dd>{formatPointAmount(metrics.consumption.other_consumer_income)}</dd></div>
                <div><dt>自有调用名义收入</dt><dd>{formatPointAmount(metrics.consumption.own_usage_income)}</dd></div>
                <div><dt>平台手续费</dt><dd>{formatPointAmount(metrics.consumption.platform_fee)}</dd></div>
              </dl>
            </article>
            <article className="panel projection-panel">
              <header className="panel-heading"><h2>积分集中度</h2></header>
              <dl className="ops-list">
                <div><dt>正余额用户</dt><dd>{metrics.concentration.positive_user_count}</dd></div>
                <div><dt>正余额合计</dt><dd>{formatPointAmount(metrics.concentration.total_positive)}</dd></div>
                <div><dt>Top 1 / Top 5 占比</dt><dd>{formatShare(metrics.concentration.top1_share)} / {formatShare(metrics.concentration.top5_share)}</dd></div>
                <div><dt>HHI</dt><dd>{metrics.concentration.hhi === null ? '—' : metrics.concentration.hhi}</dd></div>
              </dl>
            </article>
          </section>

          <section className="ops-grid">
            <article className="panel projection-panel">
              <header className="panel-heading"><h2>C2C 市场</h2></header>
              <dl className="ops-list">
                <div><dt>最近成交</dt><dd>{formatFen(metrics.c2c.quote.last_traded_price_fen)}</dd></div>
                <div><dt>买一 / 卖一</dt><dd>{formatFen(metrics.c2c.quote.best_bid_price_fen)} / {formatFen(metrics.c2c.quote.best_ask_price_fen)}</dd></div>
                <div><dt>价差</dt><dd>{formatFen(metrics.c2c.quote.spread_fen)}</dd></div>
                {metrics.c2c.orders.map((row) => (
                  <div key={`${row.side}-${row.status}`}>
                    <dt>{row.side === 'sell' ? '卖单' : '买单'} · {row.status}</dt>
                    <dd>{row.count}</dd>
                  </div>
                ))}
                {metrics.c2c.trades.map((row) => (
                  <div key={`trade-${row.status}`}>
                    <dt>交易 · {row.status}</dt>
                    <dd>{row.count}</dd>
                  </div>
                ))}
              </dl>
            </article>
            <article className="panel projection-panel">
              <header className="panel-heading"><h2>负余额风险（不预设逾期）</h2></header>
              {metrics.negative_balances.length === 0 ? (
                <p className="muted">当前没有负余额账户。</p>
              ) : (
                <table className="data-table">
                  <thead>
                    <tr><th>账户</th><th>余额</th><th>进入负数</th><th>最后活动</th><th>不活跃天数</th></tr>
                  </thead>
                  <tbody>
                    {metrics.negative_balances.map((row) => (
                      <tr key={row.account_id}>
                        <td>{row.username}{row.over_limit ? '（超限）' : ''}</td>
                        <td>{formatPointAmount(row.posted_balance)}</td>
                        <td>{row.negative_since ? new Date(row.negative_since).toLocaleString() : '—'}</td>
                        <td>{row.last_financial_activity ? new Date(row.last_financial_activity).toLocaleString() : '—'}</td>
                        <td>{row.inactive_days}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </article>
          </section>

          <section className="panel table-panel" aria-label="巡检历史">
            <header className="table-toolbar">
              <h2>跨模块巡检历史</h2>
              <small>启动与每小时自动执行，可手动触发</small>
            </header>
            {inspections.length === 0 ? (
              <p className="muted">还没有巡检记录。</p>
            ) : (
              <table className="data-table">
                <thead>
                  <tr><th>时间</th><th>触发</th><th>零和</th><th>投影</th><th>调用结算</th><th>C2C 一致</th><th>差异</th></tr>
                </thead>
                <tbody>
                  {inspections.map((row) => {
                    const ok = row.zero_sum_ok && row.projection_ok && row.call_settlement_ok && row.c2c_consistency_ok
                    return (
                      <tr key={row.id}>
                        <td>{new Date(row.checked_at).toLocaleString()}</td>
                        <td>{row.triggered_by === 'startup' ? '启动' : row.triggered_by === 'periodic' ? '周期' : '手动'}</td>
                        <td>{row.zero_sum_ok ? '正常' : '异常'}</td>
                        <td>{row.projection_ok ? '正常' : '异常'}</td>
                        <td>{row.call_settlement_ok ? '正常' : '异常'}</td>
                        <td>{row.c2c_consistency_ok ? '正常' : '异常'}</td>
                        <td>{ok ? '0' : `${row.successful_calls_without_settlement + row.settlements_without_ledger_transaction + row.c2c_quantity_violations + row.c2c_hold_violations}`}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            )}
          </section>

          {trial && (
            <section className="panel" aria-label="试用证据摘要">
              <header className="panel-heading">
                <h2>试用证据摘要</h2>
                <small>仅聚合计数与状态；不宣称参与者为真人或人民币已到账</small>
              </header>
              <dl className="ops-list">
                <div><dt>非管理员账户</dt><dd>{trial.non_admin_accounts}</dd></div>
                <div><dt>已发布渠道 / 通过报价 / 活跃 Key</dt><dd>{trial.published_channels} / {trial.passed_offers} / {trial.active_api_keys}</dd></div>
                <div><dt>调用成功 / 失败 / 不完整</dt><dd>{trial.calls_succeeded} / {trial.calls_failed} / {trial.calls_incomplete}</dd></div>
                <div><dt>首次调用</dt><dd>{trial.first_call_at ? new Date(trial.first_call_at).toLocaleString() : '尚无调用'}</dd></div>
                <div><dt>C2C 挂单 / 完成交易 / 争议中</dt><dd>{trial.c2c_open_orders} / {trial.c2c_released_trades} / {trial.c2c_disputed_open}</dd></div>
                <div><dt>零和状态</dt><dd>{trial.ledger_zero_sum_ok ? '正常' : '异常'}</dd></div>
                <div><dt>巡检通过</dt><dd>{trial.inspection_pass_count} / {trial.inspection_total_count}</dd></div>
              </dl>
            </section>
          )}

          <section className="ops-grid">
            <article className="panel projection-panel">
              <header className="panel-heading"><h2>一致性校验</h2></header>
              <dl className="ops-list">
                <div><dt>余额投影差额</dt><dd>{formatPointAmount(metrics.ledger.posted_projection_difference)}</dd></div>
                <div><dt>余额投影异常账户</dt><dd>{metrics.ledger.posted_projection_mismatch_accounts}</dd></div>
                <div><dt>资产冻结投影差额</dt><dd>{formatPointAmount(metrics.ledger.asset_reservation_difference)}</dd></div>
                <div><dt>消费授权投影差额</dt><dd>{formatPointAmount(metrics.ledger.spend_authorization_difference)}</dd></div>
                <div><dt>持有投影异常账户</dt><dd>{metrics.ledger.hold_projection_mismatch_accounts}</dd></div>
              </dl>
            </article>
            <article className="panel projection-panel">
              <header className="panel-heading"><h2>持有中的积分</h2></header>
              <dl className="ops-list">
                <div><dt>资产冻结</dt><dd>{formatPointAmount(metrics.ledger.asset_reserved)}</dd></div>
                <div><dt>消费授权</dt><dd>{formatPointAmount(metrics.ledger.spend_authorized)}</dd></div>
                <div><dt>账本账户</dt><dd>{metrics.ledger.ledger_account_count}</dd></div>
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
