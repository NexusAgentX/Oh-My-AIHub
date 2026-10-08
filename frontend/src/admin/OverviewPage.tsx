import { Badge, ButtonLink, Card, Metric, PageHeader, QueryBoundary, type BadgeTone } from '../ui'
import { formatCount, formatPoints, formatRatio, pointsRatio } from './format'
import { absolute } from './pointsView'
import { useAdminOverview, useAdminPoints } from './queries'
import type { AdminOverview, AttentionItem } from './types'

const attentionKinds: Record<AttentionItem['kind'], { label: string; action: string }> = {
  dispute: { label: '申诉', action: '去仲裁' },
  over_limit: { label: '透支', action: '查看用户' },
  negative_balance: { label: '负余额过久', action: '查看用户' },
  channel_suspended: { label: '渠道下架', action: '查看渠道' },
  channel_failing: { label: '渠道异常', action: '查看渠道' },
  ledger_unbalanced: { label: '核对不通过', action: '去核对' },
  stuck_call: { label: '调用未结束', action: '查看调用' },
}

function attentionMeta(item: AttentionItem) {
  return attentionKinds[item.kind] ?? { label: item.kind, action: '查看' }
}

/** 「需要处理」列表：每项一个类型徽标 + 一句话 + 操作。 */
export function AttentionList({ items }: { items: AttentionItem[] }) {
  if (items.length === 0) {
    return (
      <p className="attention-empty">
        <Badge tone="success">✓</Badge> 暂无需要处理的事
      </p>
    )
  }
  const sorted = [...items].sort((left, right) => (left.severity === right.severity ? 0 : left.severity === 'critical' ? -1 : 1))
  return (
    <ul className="attention-list">
      {sorted.map((item, index) => {
        const meta = attentionMeta(item)
        const tone: BadgeTone = item.severity === 'critical' ? 'danger' : 'warning'
        return (
          <li key={`${item.kind}-${index}`}>
            <Badge tone={tone}>{meta.label}</Badge>
            <span className="attention-title">
              {item.title}
              {item.count > 1 && <span className="muted"> · {item.count} 项</span>}
            </span>
            <ButtonLink size="sm" to={item.link}>
              {meta.action}
            </ButtonLink>
          </li>
        )
      })}
    </ul>
  )
}

function CreditMetric() {
  const points = useAdminPoints()
  if (!points.data) {
    return <Metric hint={points.isError ? '暂不可用' : '加载中'} label="信用占用" value="—" />
  }
  const { balances } = points.data
  const used = absolute(balances.user_negative)
  return (
    <Metric
      hint={`已用 ${formatPoints(used)} / 总额度 ${formatPoints(balances.credit_issued)}`}
      label="信用占用"
      progress={pointsRatio(used, balances.credit_issued)}
      value={`${Math.round(pointsRatio(used, balances.credit_issued))}%`}
    />
  )
}

function Indicators({ overview }: { overview: AdminOverview }) {
  const { ledger, today, c2c } = overview
  return (
    <section aria-label="指标" className="metric-grid">
      <Metric
        hint={`全部余额合计 ${formatPoints(ledger.total)}`}
        label="账本"
        tone={ledger.balanced ? 'accent' : 'warm'}
        value={ledger.balanced ? '平衡' : '不平衡'}
      />
      <CreditMetric />
      <Metric hint={`成功率 ${formatRatio(today.success_rate)}`} label="今日调用" value={formatCount(today.calls)} />
      <Metric
        hint={`卖单 ${c2c.open_orders} · 待付款 ${c2c.awaiting_payment}`}
        label="C2C 申诉中"
        value={formatCount(c2c.open_disputes)}
      />
    </section>
  )
}

export function OverviewPage() {
  const overview = useAdminOverview()
  return (
    <>
      <PageHeader title="概览" />
      <QueryBoundary query={overview}>
        {(data) => (
          <div className="admin-stack">
            <Card title="需要处理">
              <AttentionList items={data.attention} />
            </Card>
            <Indicators overview={data} />
          </div>
        )}
      </QueryBoundary>
    </>
  )
}
