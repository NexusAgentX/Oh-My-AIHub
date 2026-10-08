import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import type { OpsMetrics } from '../api/types'
import { Card, DataTable, EmptyState, Metric, MetricGrid, QueryBoundary } from '../ui'
import { formatPointAmount } from '../wallet/presentation'
import { formatDateTime, formatFen, formatShare } from './opsFormat'
import { useOpsMetricsQuery } from './queries'

function Rows({ children }: { children: ReactNode }) {
  return <dl className="ops-list">{children}</dl>
}

function Row({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd className="num">{children}</dd>
    </div>
  )
}

export function OpsOverview({ hours }: { hours: number }) {
  const query = useOpsMetricsQuery(hours)
  return (
    <QueryBoundary errorFallback="运营指标加载失败" query={query}>
      {(metrics) => <OverviewContent metrics={metrics} />}
    </QueryBoundary>
  )
}

function OverviewContent({ metrics }: { metrics: OpsMetrics }) {
  const { ledger, api, consumption, concentration, c2c } = metrics
  const successRate =
    api.success_rate === null ? '空样本' : `${(Number(api.success_rate) * 100).toFixed(2)}%`
  return (
    <div className="ops-stack">
      <MetricGrid label="账本核心指标">
        <Metric
          hint={ledger.zero_sum ? '零和正常' : '需要处理'}
          label="全账户余额和"
          tone="accent"
          value={formatPointAmount(ledger.total_posted_balance)}
        />
        <Metric
          hint={`负 ${formatPointAmount(ledger.negative_posted_balance)}`}
          label="正余额"
          value={formatPointAmount(ledger.positive_posted_balance)}
        />
        <Metric
          hint={`额度 ${formatPointAmount(ledger.total_credit_limit)} · 已用 ${formatPointAmount(ledger.credit_capacity_used)}`}
          label="有效信用"
          tone="warm"
          value={formatPointAmount(metrics.effective_credit)}
        />
        <Metric
          hint={`${ledger.credit_frozen_accounts} 个信用冻结`}
          label="信用风险账户"
          value={ledger.over_limit_accounts}
        />
      </MetricGrid>

      <MetricGrid label="API 调用窗口指标">
        <Metric
          hint={`到达上游且已终态 ${api.terminal_reached} 次`}
          label="调用成功率"
          value={successRate}
        />
        <Metric hint="零上游、零 hold" label="预校验拒绝" value={api.precheck_rejected} />
        <Metric
          hint={`全部失败 ${api.all_failed} · 提交后不完整 ${api.incomplete_after_commit}`}
          label="到达上游 / 成功"
          value={`${api.reached_upstream} / ${api.succeeded}`}
        />
        <Metric
          hint={
            api.average_tokens_per_second !== null
              ? `${api.average_tokens_per_second} tok/s`
              : '空样本显示 —'
          }
          label="平均 TTFT / TPS"
          value={
            api.average_ttft_milliseconds !== null ? `${api.average_ttft_milliseconds} ms` : '—'
          }
        />
      </MetricGrid>

      <div className="ops-grid">
        <Card title="消费与收入">
          <Rows>
            <Row label="消费支出">{formatPointAmount(consumption.consumer_spend)}</Row>
            <Row label="共享者收入（全部）">{formatPointAmount(consumption.provider_income)}</Row>
            <Row label="来自其他消费者">{formatPointAmount(consumption.other_consumer_income)}</Row>
            <Row label="自有调用名义收入">{formatPointAmount(consumption.own_usage_income)}</Row>
            <Row label="平台手续费">{formatPointAmount(consumption.platform_fee)}</Row>
          </Rows>
        </Card>
        <Card title="积分集中度">
          <Rows>
            <Row label="正余额用户">{concentration.positive_user_count}</Row>
            <Row label="正余额合计">{formatPointAmount(concentration.total_positive)}</Row>
            <Row label="Top 1 / Top 5 占比">
              {formatShare(concentration.top1_share)} / {formatShare(concentration.top5_share)}
            </Row>
            <Row label="HHI">{concentration.hhi ?? '—'}</Row>
          </Rows>
        </Card>
      </div>

      <div className="ops-grid">
        <Card title="C2C 市场">
          <Rows>
            <Row label="最近成交">{formatFen(c2c.quote.last_traded_price_fen)}</Row>
            <Row label="卖一">{formatFen(c2c.quote.best_ask_price_fen)}</Row>
            {c2c.orders.map((row) => (
              <Row key={row.status} label={`卖单 · ${row.status}`}>
                {row.count}
              </Row>
            ))}
            {c2c.trades.map((row) => (
              <Row key={`trade-${row.status}`} label={`交易 · ${row.status}`}>
                {row.count}
              </Row>
            ))}
          </Rows>
        </Card>
        <Card flush title="负余额风险">
          <DataTable
            caption="负余额账户"
            columns={[
              {
                key: 'account',
                header: '账户',
                primary: true,
                cell: (row) => (
                  <Link to={`/admin/ledger/accounts/${row.account_id}`}>
                    {row.username}
                    {row.over_limit ? '（超限）' : ''}
                  </Link>
                ),
              },
              {
                key: 'balance',
                header: '余额',
                numeric: true,
                cell: (row) => formatPointAmount(row.posted_balance),
              },
              {
                key: 'since',
                header: '进入负数',
                cell: (row) => formatDateTime(row.negative_since),
              },
              {
                key: 'inactive',
                header: '不活跃天数',
                numeric: true,
                cell: (row) => row.inactive_days,
              },
            ]}
            empty={<EmptyState title="当前没有负余额账户" />}
            rowKey={(row) => row.account_id}
            rows={metrics.negative_balances}
          />
        </Card>
      </div>
    </div>
  )
}
