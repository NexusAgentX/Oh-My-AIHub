import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
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
import { daysSince, formatDateTime, formatPoints } from './format'
import { LineChart } from './LineChart'
import {
  absolute,
  holdingConcentration,
  ledgerChecks,
  ledgerEquation,
  toNumber,
  trendWindow,
  type TrendWindow,
} from './pointsView'
import {
  useAdminAccounts,
  useAdminAudit,
  useAdminOverview,
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
import type { AdminPoints, AdminPointsRisk, AuditEntry, LedgerTransaction, TransactionType } from './types'

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
  const overview = useAdminOverview()
  const c2c = overview.data?.c2c
  const { balances } = points
  return (
    <>
      <section aria-label="积分全局" className="metric-grid metric-grid-five">
        <Metric label="流通积分" tone="accent" value={formatPoints(balances.user_positive)} hint="用户正余额合计" />
        <Metric
          label="信用发行"
          value={formatPoints(absolute(balances.user_negative))}
          hint={`负余额合计 · 总额度 ${formatPoints(balances.credit_issued)}`}
        />
        <Metric
          label="C2C 托管"
          value={formatPoints(balances.c2c_escrow)}
          hint={c2c ? `卖单 ${c2c.open_orders} · 待付款 ${c2c.awaiting_payment}` : undefined}
        />
        <Metric label="平台收入" value={formatPoints(balances.platform_revenue)} />
        <Metric label="坏账" value={formatPoints(balances.bad_debt)} hint="已核销转入" />
      </section>
      <LedgerEquation balances={balances} />
    </>
  )
}

const trendGroups = {
  balances: [
    { key: 'circulation', label: '流通', color: 'var(--ink)' },
    { key: 'c2c_escrow', label: 'C2C 托管', color: 'var(--mustard)' },
  ],
  income: [
    { key: 'platform_revenue', label: '平台收入', color: 'var(--positive)' },
    { key: 'bad_debt', label: '坏账', color: 'var(--danger)' },
  ],
} as const

function Trend({ trend }: { trend: AdminPoints['trend'] }) {
  const [days, setDays] = useState<TrendWindow>(30)
  const [group, setGroup] = useState<keyof typeof trendGroups>('balances')
  const points = trendWindow(trend, days)
  return (
    <Card
      className="chart-card"
      actions={
        <div className="chart-controls">
          <Segmented
            label="指标"
            onChange={setGroup}
            options={[
              { key: 'balances', label: '流通与托管' },
              { key: 'income', label: '收入与坏账' },
            ]}
            value={group}
          />
          <Segmented
            label="时间窗"
            onChange={(key) => setDays(Number(key) as TrendWindow)}
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
      <LineChart
        labels={points.map((point) => point.date)}
        series={trendGroups[group].map((item) => ({
          ...item,
          values: points.map((point) => toNumber(point[item.key])),
        }))}
        title={`最近 ${days} 天走势`}
      />
    </Card>
  )
}

function Checks({ points }: { points: AdminPoints }) {
  const [repairOpen, setRepairOpen] = useState(false)
  const [callID, setCallID] = useState('')
  const [action, setAction] = useState<'charge' | 'void'>('charge')
  const repair = useRepairCall()
  const rows = ledgerChecks(points.check)
  return (
    <Card
      actions={
        <Button onClick={() => setRepairOpen(true)} size="sm" type="button" variant="secondary">
          补记调用
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
            <strong className="num">{row.ok ? formatPoints(row.value) : `差额 ${formatPoints(row.value)}`}</strong>
            <span className="visually-hidden">{row.ok ? '通过' : '不通过'}</span>
            {!row.ok && row.link && <Link to={row.link}>明细</Link>}
          </li>
        ))}
      </ul>
      <p className="muted-copy">核对时间 {formatDateTime(points.check.checked_at)}</p>
      <ConfirmActionDialog
        confirmLabel={action === 'charge' ? '确认补记' : '确认作废'}
        onClose={() => setRepairOpen(false)}
        onConfirm={(reason) => repair.mutateAsync({ id: callID.trim(), body: { action, reason } })}
        open={repairOpen}
        summary={
          <p>
            {action === 'charge' ? '按调用记录的用量与价格快照补记账' : '标记为不收费的中断'}：调用{' '}
            <span className="mono">{callID.trim()}</span>
          </p>
        }
        title="补记调用"
        validate={() =>
          /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(callID.trim()) ? '' : '请输入完整的请求 ID'
        }
      >
        <TextField label="请求 ID" onChange={(event) => setCallID(event.target.value)} value={callID} />
        <SelectField label="处理方式" onChange={(event) => setAction(event.target.value as 'charge' | 'void')} value={action}>
          <option value="charge">补记账</option>
          <option value="void">作废（不收费）</option>
        </SelectField>
      </ConfirmActionDialog>
    </Card>
  )
}

const riskColumns: Column<AdminPointsRisk>[] = [
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
      risk.kind === 'over_limit' ? <Badge tone="danger">超出信用额度</Badge> : <Badge tone="warning">负余额且不活跃</Badge>,
  },
  { key: 'balance', header: '余额', numeric: true, cell: (risk) => <span className="amount-negative">{formatPoints(risk.balance)}</span> },
  { key: 'limit', header: '信用额度', numeric: true, cell: (risk) => formatPoints(risk.credit_limit) },
  {
    key: 'activity',
    header: '最近活跃',
    numeric: true,
    cell: (risk) => {
      const days = daysSince(risk.last_activity_at)
      return days === null ? '—' : `${days} 天前`
    },
  },
]

function Risks({ points }: { points: AdminPoints }) {
  const accounts = useAdminAccounts({})
  const concentration = accounts.data ? holdingConcentration(accounts.data, points.balances.user_positive) : null
  return (
    <Card flush title="风险">
      {concentration && (
        <p className="risk-concentration">
          持有集中度：前 5 名占流通 <strong className="num">{concentration.topShare}%</strong>
          {concentration.largest && (
            <>
              ，最大持有 {concentration.largest.name}{' '}
              <Badge tone={concentration.largest.share > 50 ? 'danger' : 'neutral'}>{concentration.largest.share}%</Badge>
            </>
          )}
          {accounts.hasNextPage && <span className="muted-copy">（按已加载的账户计算）</span>}
        </p>
      )}
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

function Overview() {
  const points = useAdminPoints()
  return (
    <QueryBoundary query={points}>
      {(data) => (
        <div className="admin-stack">
          <Figures points={data} />
          <Trend trend={data.trend} />
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

function Transactions() {
  const [params, setParams] = useSearchParams()
  const accountID = params.get('account_id') ?? ''
  const selected = params.get('transaction')
  const [type, setType] = useState<TransactionType | ''>('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const accounts = useAdminAccounts({})
  const transactions = useAdminTransactions({
    account_id: accountID || undefined,
    type: type || undefined,
    from: dateParam(from),
    to: dateParam(to, true),
  })
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
