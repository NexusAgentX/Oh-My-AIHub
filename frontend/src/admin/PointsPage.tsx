import { useEffect, useState } from 'react'
import { Link, useLocation, useSearchParams } from 'react-router-dom'
import {
  Badge,
  Button,
  Card,
  DataTable,
  Drawer,
  Metric,
  PageHeader,
  QueryBoundary,
  Segmented,
  SelectField,
  Tabs,
  TextField,
  Toolbar,
  type Column,
} from '../ui'
import { ConfirmActionDialog, LoadMore } from './components'
import { daysSince, formatDateTime, formatPoints, formatRatio } from './format'
import { LineChart } from './LineChart'
import { ledgerChecks, ledgerEquation, sortedTrend, type TrendWindow } from './pointsView'
import {
  useAdminAccounts,
  useAdminAudit,
  useAdminPoints,
  useAdminTransactions,
  useRepairCall,
} from './queries'
import {
  auditActionLabel,
  ledgerAccountLabel,
  RelatedLink,
  TransactionDrawerBody,
  TransactionTypeLabel,
  transactionTypeLabels,
  transactionVolume,
} from './transactions'
import type { AdminPoints, AdminPointsRisk, AuditEntry, LedgerRelated, LedgerTransaction, TransactionType } from './types'

type RelatedType = LedgerRelated['type']

/** 等式行：用户 + C2C 托管 + 平台收入 + 坏账 = 合计。 */
export function LedgerEquation({ balances }: { balances: AdminPoints['balances'] }) {
  const equation = ledgerEquation(balances)
  return (
    <p className={`ledger-equation ${equation.balanced ? '' : 'ledger-equation-broken'}`}>
      {equation.terms.map((term, index) => (
        <span key={term.label}>
          {index > 0 && <span className="equation-op">+</span>}
          <span className="equation-term">
            <small>{term.label}</small>
            <strong className="num">{formatPoints(term.value)}</strong>
          </span>
        </span>
      ))}
      <span className="equation-op">=</span>
      <span className="equation-term">
        <small>合计</small>
        <strong className="num">{formatPoints(equation.total)}</strong>
      </span>
      <Badge tone={equation.balanced ? 'success' : 'danger'}>{equation.balanced ? '✓ 平衡' : '不平衡'}</Badge>
    </p>
  )
}

function Figures({ points }: { points: AdminPoints }) {
  const { balances } = points
  return (
    <>
      <section aria-label="积分全局" className="metric-grid metric-grid-five">
        <Metric label="流通积分" tone="accent" value={formatPoints(balances.user_positive)} hint="用户正余额合计" />
        <Metric
          label="信用发行"
          value={formatPoints(balances.credit_issued)}
          hint={`总额度 ${formatPoints(balances.total_credit_limit)}`}
        />
        <Metric
          label="C2C 托管"
          value={formatPoints(balances.c2c_escrow)}
          hint={`卖单 ${balances.escrow_orders} · 交易中 ${balances.escrow_trades_in_progress}`}
        />
        <Metric label="平台收入" value={formatPoints(balances.platform_revenue)} />
        <Metric label="坏账" value={formatPoints(balances.bad_debt)} hint={`已核销 ${balances.bad_debt_writeoffs} 笔`} />
      </section>
      <LedgerEquation balances={balances} />
    </>
  )
}

type TrendPoint = AdminPoints['trend'][number]
type TrendSeries = { key: string; label: string; color: string; value: (point: TrendPoint) => number | null }

const amount = (key: 'circulation' | 'credit_issued' | 'c2c_escrow' | 'platform_revenue' | 'bad_debt' | 'api_volume' | 'api_fee' | 'c2c_volume') =>
  (point: TrendPoint) => Number(point[key])

export const trendGroups: Record<'balances' | 'income' | 'api' | 'c2c' | 'price', { label: string; series: TrendSeries[] }> = {
  balances: {
    label: '流通与信用',
    series: [
      { key: 'circulation', label: '流通', color: 'var(--ink)', value: amount('circulation') },
      { key: 'credit_issued', label: '信用发行', color: 'var(--danger)', value: amount('credit_issued') },
      { key: 'c2c_escrow', label: 'C2C 托管', color: 'var(--mustard)', value: amount('c2c_escrow') },
    ],
  },
  income: {
    label: '收入与坏账',
    series: [
      { key: 'platform_revenue', label: '平台收入', color: 'var(--positive)', value: amount('platform_revenue') },
      { key: 'bad_debt', label: '坏账', color: 'var(--danger)', value: amount('bad_debt') },
    ],
  },
  api: {
    label: 'API 结算',
    series: [
      { key: 'api_volume', label: 'API 结算量', color: 'var(--ink)', value: amount('api_volume') },
      { key: 'api_fee', label: '手续费', color: 'var(--positive)', value: amount('api_fee') },
    ],
  },
  c2c: {
    label: 'C2C 成交量',
    series: [{ key: 'c2c_volume', label: 'C2C 成交量', color: 'var(--mustard)', value: amount('c2c_volume') }],
  },
  price: {
    label: 'C2C 均价',
    series: [
      {
        key: 'c2c_avg_price',
        label: 'C2C 均价（元/积分）',
        color: 'var(--ink)',
        value: (point) => (point.c2c_avg_price_fen === null ? null : point.c2c_avg_price_fen / 100),
      },
    ],
  },
}

function Trend({
  trend,
  days,
  onDays,
  loading,
}: {
  trend: AdminPoints['trend']
  days: TrendWindow
  onDays: (days: TrendWindow) => void
  loading: boolean
}) {
  const [group, setGroup] = useState<keyof typeof trendGroups>('balances')
  const points = sortedTrend(trend)
  return (
    <Card
      className="chart-card"
      actions={
        <div className="chart-controls">
          <SelectField label="指标" onChange={(event) => setGroup(event.target.value as keyof typeof trendGroups)} value={group}>
            {(Object.keys(trendGroups) as Array<keyof typeof trendGroups>).map((key) => (
              <option key={key} value={key}>
                {trendGroups[key].label}
              </option>
            ))}
          </SelectField>
          <Segmented
            label="时间窗"
            onChange={(key) => onDays(Number(key) as TrendWindow)}
            options={[
              { key: '7', label: '7 天' },
              { key: '30', label: '30 天' },
              { key: '90', label: '90 天' },
            ]}
            value={String(days)}
          />
        </div>
      }
      title="走势"
    >
      <div aria-busy={loading || undefined}>
        <LineChart
          labels={points.map((point) => point.date)}
          series={trendGroups[group].series.map((item) => ({
            key: item.key,
            label: item.label,
            color: item.color,
            values: points.map(item.value),
          }))}
          title={`最近 ${days} 天走势`}
        />
      </div>
    </Card>
  )
}

type Repair = { id: string; action: 'charge' | 'void' }

function MissingCalls({ points, onRepair }: { points: AdminPoints; onRepair: (repair: Repair) => void }) {
  const billing = points.checks.billing_calls
  if (billing.passed) return null
  return (
    <div className="check-detail">
      <h3 className="section-title">
        漏记调用 {billing.missing_count > billing.missing.length && <small className="muted">（显示前 {billing.missing.length} 条，共 {billing.missing_count} 条）</small>}
      </h3>
      <ul className="plain-list missing-calls">
        {billing.missing.map((call) => (
          <li key={call.call_id}>
            <span>
              <Link className="mono" to={`/admin/calls?call=${call.call_id}`}>
                {call.call_id.slice(0, 8)}
              </Link>{' '}
              <span className="muted-copy">
                {formatDateTime(call.created_at)} · {call.account.display_name} · {call.channel.name} · {formatPoints(call.charged)}
              </span>
            </span>
            <span className="missing-call-actions">
              <Button onClick={() => onRepair({ id: call.call_id, action: 'charge' })} size="sm" type="button">
                补记
              </Button>
              <Button onClick={() => onRepair({ id: call.call_id, action: 'void' })} size="sm" type="button" variant="quiet">
                作废
              </Button>
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

function CheckDetails({ points }: { points: AdminPoints }) {
  const { account_balances: accounts, escrow, released_trades: trades } = points.checks
  return (
    <>
      {!accounts.passed && (
        <div className="check-detail">
          <h3 className="section-title">余额与分录不一致的账户</h3>
          <ul className="plain-list">
            {accounts.mismatches.map((row, index) => (
              <li key={index}>
                <strong>{ledgerAccountLabel(row.ledger_account)}</strong>
                <span className="muted-copy num">
                  余额 {formatPoints(row.balance)} · 分录合计 {formatPoints(row.entries_total)}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {!escrow.passed && (
        <p className="check-detail muted-copy num">
          托管余额 {formatPoints(escrow.escrow_balance)} · 卖单合计 {formatPoints(escrow.orders_total)} · 差额{' '}
          {formatPoints(escrow.difference)}
        </p>
      )}
      {!trades.passed && (
        <div className="check-detail">
          <h3 className="section-title">漏记的 C2C 交易</h3>
          <ul className="plain-list">
            {trades.missing.map((trade) => (
              <li key={trade.trade_id}>
                <Link className="mono" to={`/admin/disputes/${trade.trade_id}`}>
                  {trade.trade_id.slice(0, 8)}
                </Link>
                <span className="muted-copy">
                  {formatPoints(trade.amount)} 积分 · {formatDateTime(trade.resolved_at)}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </>
  )
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

function Checks({ points }: { points: AdminPoints }) {
  const [repair, setRepair] = useState<Repair | null>(null)
  const [manual, setManual] = useState(false)
  const repairCall = useRepairCall()
  const rows = ledgerChecks(points.check, points.checks)
  const close = () => {
    setRepair(null)
    setManual(false)
  }
  return (
    <Card
      actions={
        <Button
          onClick={() => {
            setManual(true)
            setRepair({ id: '', action: 'charge' })
          }}
          size="sm"
          type="button"
          variant="secondary"
        >
          按请求 ID 补记
        </Button>
      }
      title="实时核对"
    >
      <ul className="check-list" id="checks">
        {rows.map((row) => (
          <li className={row.ok ? 'check-ok' : 'check-failed'} key={row.key}>
            <span aria-hidden="true" className="check-mark">
              {row.ok ? '✓' : '✗'}
            </span>
            <span className="check-label">{row.label}</span>
            <strong className="num">{row.detail}</strong>
            <span className="visually-hidden">{row.ok ? '通过' : '不通过'}</span>
            {!row.ok && row.key === 'zero_sum' && <Link to="/admin/points?tab=transactions">明细</Link>}
          </li>
        ))}
      </ul>
      <MissingCalls onRepair={setRepair} points={points} />
      <CheckDetails points={points} />
      <p className="muted-copy">核对时间 {formatDateTime(points.checks.checked_at)}</p>
      <ConfirmActionDialog
        confirmLabel={repair?.action === 'void' ? '确认作废' : '确认补记'}
        onClose={close}
        onConfirm={async (reason) => {
          if (!repair) return
          await repairCall.mutateAsync({ id: repair.id.trim(), body: { action: repair.action, reason } })
        }}
        open={repair !== null}
        summary={
          <p>
            {repair?.action === 'void' ? '标记为不收费的中断' : '按调用记录的用量与价格快照补记账'}：调用{' '}
            <span className="mono">{repair?.id.trim()}</span>
          </p>
        }
        title={repair?.action === 'void' ? '作废调用' : '补记调用'}
        validate={() => (uuidPattern.test(repair?.id.trim() ?? '') ? '' : '请输入完整的请求 ID')}
      >
        {manual && repair && (
          <>
            <TextField label="请求 ID" onChange={(event) => setRepair({ ...repair, id: event.target.value })} value={repair.id} />
            <SelectField
              label="处理方式"
              onChange={(event) => setRepair({ ...repair, action: event.target.value as 'charge' | 'void' })}
              value={repair.action}
            >
              <option value="charge">补记账</option>
              <option value="void">作废（不收费）</option>
            </SelectField>
          </>
        )}
      </ConfirmActionDialog>
    </Card>
  )
}

export const riskColumns: Column<AdminPointsRisk>[] = [
  {
    key: 'account',
    header: '用户',
    primary: true,
    cell: (risk) => (
      <>
        <strong>{risk.account.display_name}</strong>
        <small className="mono">@{risk.account.username}</small>
      </>
    ),
  },
  {
    key: 'kind',
    header: '风险',
    cell: (risk) =>
      risk.kind === 'over_limit' ? <Badge tone="danger">超出信用额度</Badge> : <Badge tone="warning">长期负余额</Badge>,
  },
  { key: 'balance', header: '余额', numeric: true, cell: (risk) => <span className="amount-negative">{formatPoints(risk.balance)}</span> },
  { key: 'limit', header: '信用额度', numeric: true, cell: (risk) => formatPoints(risk.credit_limit) },
  {
    key: 'negative_days',
    header: '负余额天数',
    numeric: true,
    cell: (risk) => (risk.negative_days === null ? '—' : `${risk.negative_days} 天`),
  },
  {
    key: 'activity',
    header: '最近活跃',
    numeric: true,
    hideOnMobile: true,
    cell: (risk) => {
      const days = daysSince(risk.last_activity_at)
      return days === null ? '—' : days === 0 ? '今天' : `${days} 天前`
    },
  },
]

function Risks({ points }: { points: AdminPoints }) {
  const { concentration } = points
  const largest = concentration.top[0]
  return (
    <Card flush title="风险">
      <div className="risk-concentration" id="risks">
        <p>
          持有集中度：前 5 名占流通 <strong className="num">{formatRatio(concentration.top5_share)}</strong>
          {largest && (
            <>
              ，最大持有 {largest.account.display_name}{' '}
              <Badge tone={Number(largest.share) > 0.5 ? 'danger' : 'neutral'}>{formatRatio(largest.share)}</Badge>
            </>
          )}
        </p>
        {concentration.top.length > 1 && (
          <ol className="concentration-list">
            {concentration.top.map((holder) => (
              <li key={holder.account.id}>
                <span>{holder.account.display_name}</span>
                <span className="num muted-copy">
                  {formatPoints(holder.balance)} · {formatRatio(holder.share)}
                </span>
              </li>
            ))}
          </ol>
        )}
      </div>
      <DataTable
        caption="风险账户"
        columns={riskColumns}
        empty={<p className="muted-copy empty-pad">没有超额或长期负余额的账户</p>}
        rowKey={(risk) => `${risk.kind}-${risk.account.id}`}
        rows={points.risks}
      />
    </Card>
  )
}

/** 概览「需要处理」跳转带锚点（#checks / #risks）时，数据就绪后滚动到对应区块。 */
function useScrollToHash(ready: boolean) {
  const { hash } = useLocation()
  useEffect(() => {
    if (!ready || !hash) return
    document.getElementById(hash.slice(1))?.scrollIntoView({ block: 'start' })
  }, [ready, hash])
}

function Overview() {
  const [days, setDays] = useState<TrendWindow>(30)
  const points = useAdminPoints(days)
  useScrollToHash(Boolean(points.data))
  return (
    <QueryBoundary query={points}>
      {(data) => (
        <div className="admin-stack">
          <Figures points={data} />
          <Trend days={days} loading={points.isPlaceholderData} onDays={setDays} trend={data.trend} />
          <Checks points={data} />
          <Risks points={data} />
        </div>
      )}
    </QueryBoundary>
  )
}

const transactionColumns = (open: (id: string) => void): Column<LedgerTransaction>[] => [
  {
    key: 'time',
    header: '时间',
    primary: true,
    cell: (transaction) => (
      <button className="link-button" onClick={() => open(transaction.id)} type="button">
        {formatDateTime(transaction.created_at)}
      </button>
    ),
  },
  { key: 'type', header: '类型', cell: (transaction) => <TransactionTypeLabel type={transaction.type} /> },
  { key: 'related', header: '关联对象', cell: (transaction) => <RelatedLink related={transaction.related} /> },
  {
    key: 'accounts',
    header: '账户',
    cell: (transaction) => transaction.entries.map((entry) => ledgerAccountLabel(entry.ledger_account)).join(' → '),
  },
  { key: 'amount', header: '金额', numeric: true, cell: (transaction) => formatPoints(transactionVolume(transaction)) },
]

function dateParam(value: string, endOfDay = false) {
  if (!value) return undefined
  const date = new Date(`${value}T00:00:00+08:00`)
  if (endOfDay) date.setDate(date.getDate() + 1)
  return date.toISOString()
}

const relatedTypeLabels: Record<RelatedType, string> = {
  call: '调用',
  c2c_order: '卖单',
  c2c_trade: 'C2C 交易',
  account: '账户',
}

function Transactions() {
  const [params, setParams] = useSearchParams()
  const accountID = params.get('account_id') ?? ''
  const selected = params.get('transaction')
  const relatedType = (params.get('related_type') ?? '') as RelatedType | ''
  const relatedID = params.get('related_id') ?? ''
  const [relatedInput, setRelatedInput] = useState(relatedID)
  const [type, setType] = useState<TransactionType | ''>('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const accounts = useAdminAccounts({})
  const relatedReady = Boolean(relatedType) && uuidPattern.test(relatedID)
  const transactions = useAdminTransactions({
    account_id: accountID || undefined,
    type: type || undefined,
    related_type: relatedReady ? relatedType || undefined : undefined,
    related_id: relatedReady ? relatedID : undefined,
    from: dateParam(from),
    to: dateParam(to, true),
  })
  const relatedError = relatedType && relatedInput.trim() && !uuidPattern.test(relatedInput.trim()) ? '请输入完整的 ID' : ''
  const update = (key: string, value: string | null) =>
    setParams(
      (current) => {
        const next = new URLSearchParams(current)
        if (value) next.set(key, value)
        else next.delete(key)
        return next
      },
      { replace: true },
    )

  return (
    <>
      <Toolbar>
        <SelectField label="用户" onChange={(event) => update('account_id', event.target.value)} value={accountID}>
          <option value="">全部用户</option>
          {accountID && !accounts.data?.some((account) => account.id === accountID) && (
            <option value={accountID}>{accountID.slice(0, 8)}</option>
          )}
          {accounts.data?.map((account) => (
            <option key={account.id} value={account.id}>
              {account.display_name}（@{account.username}）
            </option>
          ))}
        </SelectField>
        <SelectField label="类型" onChange={(event) => setType(event.target.value as TransactionType | '')} value={type}>
          <option value="">全部类型</option>
          {(Object.keys(transactionTypeLabels) as TransactionType[]).map((key) => (
            <option key={key} value={key}>
              {transactionTypeLabels[key][0]}
            </option>
          ))}
        </SelectField>
        <SelectField
          label="关联对象"
          onChange={(event) => {
            update('related_type', event.target.value || null)
            if (!event.target.value) {
              setRelatedInput('')
              update('related_id', null)
            }
          }}
          value={relatedType}
        >
          <option value="">不限</option>
          {(Object.keys(relatedTypeLabels) as RelatedType[]).map((key) => (
            <option key={key} value={key}>
              {relatedTypeLabels[key]}
            </option>
          ))}
        </SelectField>
        {relatedType && (
          <TextField
            error={relatedError}
            label={`${relatedTypeLabels[relatedType]} ID`}
            onBlur={() => update('related_id', relatedInput.trim() || null)}
            onChange={(event) => setRelatedInput(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') update('related_id', relatedInput.trim() || null)
            }}
            value={relatedInput}
          />
        )}
        <TextField label="开始日期" onChange={(event) => setFrom(event.target.value)} type="date" value={from} />
        <TextField label="结束日期" onChange={(event) => setTo(event.target.value)} type="date" value={to} />
      </Toolbar>
      <Card flush>
        <QueryBoundary query={transactions}>
          {(rows) => (
            <>
              <DataTable
                caption="账本交易"
                columns={transactionColumns((id) => update('transaction', id))}
                empty={<p className="muted-copy empty-pad">没有符合条件的交易</p>}
                rowKey={(transaction) => transaction.id}
                rows={rows}
              />
              <LoadMore
                hasMore={Boolean(transactions.hasNextPage)}
                loading={transactions.isFetchingNextPage}
                onLoad={() => void transactions.fetchNextPage()}
              />
            </>
          )}
        </QueryBoundary>
      </Card>
      <Drawer onClose={() => update('transaction', null)} open={Boolean(selected)} title="交易详情">
        {selected && <TransactionDrawerBody transactionID={selected} />}
      </Drawer>
    </>
  )
}

const auditColumns: Column<AuditEntry>[] = [
  { key: 'time', header: '时间', primary: true, cell: (entry) => formatDateTime(entry.created_at) },
  { key: 'actor', header: '经办人', cell: (entry) => entry.actor?.display_name ?? '系统' },
  { key: 'action', header: '操作', cell: (entry) => auditActionLabel(entry) },
  {
    key: 'target',
    header: '对象',
    cell: (entry) => (
      <span className="mono">
        {entry.target_type}:{entry.target_id.length > 12 ? entry.target_id.slice(0, 8) : entry.target_id}
      </span>
    ),
  },
  { key: 'reason', header: '原因', cell: (entry) => entry.reason || '—' },
]

function AuditLog() {
  const audit = useAdminAudit({})
  return (
    <Card flush>
      <QueryBoundary query={audit}>
        {(rows) => (
          <>
            <DataTable
              caption="操作记录"
              columns={auditColumns}
              empty={<p className="muted-copy empty-pad">暂无操作记录</p>}
              rowKey={(entry) => entry.id}
              rows={rows}
            />
            <LoadMore hasMore={Boolean(audit.hasNextPage)} loading={audit.isFetchingNextPage} onLoad={() => void audit.fetchNextPage()} />
          </>
        )}
      </QueryBoundary>
    </Card>
  )
}

type PointsTab = 'overview' | 'transactions' | 'audit'

export function PointsPage() {
  const [params, setParams] = useSearchParams()
  const raw = params.get('tab')
  const tab: PointsTab = raw === 'transactions' || raw === 'audit' ? raw : 'overview'
  return (
    <>
      <PageHeader title="积分" />
      <Tabs
        items={[
          { key: 'overview', label: '全局' },
          { key: 'transactions', label: '交易浏览' },
          { key: 'audit', label: '操作记录' },
        ]}
        label="积分视图"
        onChange={(key) => setParams(key === 'overview' ? {} : { tab: key }, { replace: true })}
        value={tab}
      >
        {tab === 'overview' && <Overview />}
        {tab === 'transactions' && <Transactions />}
        {tab === 'audit' && <AuditLog />}
      </Tabs>
    </>
  )
}
