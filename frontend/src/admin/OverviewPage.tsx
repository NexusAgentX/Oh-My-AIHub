import { Badge, ButtonLink, Card, Metric, PageHeader, QueryBoundary, type BadgeTone } from '../ui'
import { formatCount, formatFen, formatPoints, formatRatio, pointsRatio } from './format'
import { useAdminOverview } from './queries'
import type { AdminOverview, AttentionItem } from './types'

const attentionKinds: Record<AttentionItem['kind'], { label: string; action: string; anchor?: string }> = {
  dispute: { label: '申诉', action: '去仲裁' },
  over_limit: { label: '透支', action: '查看用户' },
  negative_balance: { label: '负余额过久', action: '查看风险' },
  credit_concentration: { label: '积分集中', action: '查看集中度', anchor: 'risks' },
  channel_suspended: { label: '渠道下架', action: '查看渠道' },
  channel_failing: { label: '渠道异常', action: '查看渠道' },
  unbilled_usage: { label: '用量未读到', action: '查看调用' },
  ledger_unbalanced: { label: '账本不平衡', action: '去核对', anchor: 'checks' },
  reconciliation_failed: { label: '记账或核对异常', action: '去核对', anchor: 'checks' },
  stuck_call: { label: '调用未结束', action: '查看调用' },
}

/** 类型文案与跳转：后端给出目标页面，核对与集中度类定位到积分页对应区块。 */
export function attentionMeta(item: AttentionItem) {
  const meta = attentionKinds[item.kind] ?? { label: item.kind, action: '查看' }
  const link = item.kind === 'negative_balance' ? '/admin/points#risks' : meta.anchor && !item.link.includes('#') ? `${item.link}#${meta.anchor}` : item.link
  return { ...meta, link }
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
            <ButtonLink size="sm" to={meta.link}>
              {meta.action}
            </ButtonLink>
          </li>
        )
      })}
    </ul>
  )
}

export function Indicators({ overview }: { overview: AdminOverview }) {
  const { ledger, credit, last_24h: calls, c2c } = overview
  const creditPercent = pointsRatio(credit.issued, credit.limit)
  return (
    <section aria-label="指标" className="metric-grid">
      <Metric
        hint={`全部余额合计 ${formatPoints(ledger.total)}`}
        label="账本"
        tone={ledger.balanced ? 'accent' : 'warm'}
        value={ledger.balanced ? '平衡' : '不平衡'}
      />
      <Metric
        hint={`已用 ${formatPoints(credit.issued)} / 总额度 ${formatPoints(credit.limit)}`}
        label="信用占用"
        progress={creditPercent}
        value={`${Math.round(creditPercent)}%`}
      />
      <Metric hint={`成功率 ${formatRatio(calls.success_rate)}`} label="24 小时调用" value={formatCount(calls.calls)} />
      <Metric
        hint={`${formatPoints(c2c.volume_24h)} 积分 · 均价 ${c2c.avg_price_fen_24h === null ? '—' : formatFen(c2c.avg_price_fen_24h)}`}
        label="24 小时 C2C 成交"
        value={`${formatCount(c2c.trades_24h)} 笔`}
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
