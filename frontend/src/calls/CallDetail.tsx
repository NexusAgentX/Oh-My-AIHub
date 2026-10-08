import { PriceSnapshotView } from '../admin/transactions'
import type { CallDetail } from '../api/types'
import { formatMs, formatMultiplier, formatPoints, formatTokens } from '../money/format'
import { formatDateTime } from '../money/time'
import { CopyButton, Drawer, QueryBoundary } from '../ui'
import { AttemptTimeline } from './AttemptTimeline'
import { formatLabels, routingModeLabels } from './labels'
import { OutcomeBadge } from './OutcomeBadge'

type QueryLike<T> = {
  data: T | undefined
  isPending: boolean
  isError: boolean
  error: unknown
  refetch: () => unknown
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

/** 调用详情内容：请求信息、计价、尝试时间线。抽屉与独立页面共用。 */
export function CallDetailView({ call }: { call: CallDetail }) {
  const snapshot = call.price_snapshot
  return (
    <div className="call-detail">
      <div className="call-detail-id">
        <code className="mono">{call.id}</code>
        <CopyButton label="复制请求 ID" value={call.id} />
      </div>
      <dl className="call-detail-grid">
        <Row label="结果">
          <OutcomeBadge outcome={call.outcome} />
        </Row>
        <Row label="时间">{formatDateTime(call.created_at)}</Row>
        <Row label="模型">
          {call.model_id ?? call.requested_model}
          {call.model_id && call.requested_model !== call.model_id && (
            <small className="muted"> · 请求 {call.requested_model}</small>
          )}
        </Row>
        <Row label="格式">
          {formatLabels[call.format]} · {call.stream ? '流式' : '非流式'}
        </Row>
        <Row label="Key">{call.api_key?.name ?? '—'}</Row>
        <Row label="标签">{call.tag || '—'}</Row>
        <Row label="渠道">{call.channel?.name ?? '—'}</Row>
        <Row label="调用方式">
          {call.routing_mode ? routingModeLabels[call.routing_mode] : '—'}
          {call.routing_source === 'key' && <small className="muted"> · Key 单独设置</small>}
        </Row>
        <Row label="价格档">
          {snapshot ? `${snapshot.tier?.name ?? '基准价'} · ${formatMultiplier(snapshot.multiplier)}` : '—'}
        </Row>
        <Row label="费用">
          <span className="num">
            {formatPoints(call.charged)}
            <small className="muted">
              {' '}
              = {formatPoints(call.cost)} + 手续费 {formatPoints(call.fee)}
            </small>
          </span>
        </Row>
        <Row label="tokens">
          <span className="num">
            输入 {formatTokens(call.usage.input_tokens)} · 输出 {formatTokens(call.usage.output_tokens)}
            {(call.usage.cache_read_tokens > 0 || call.usage.cache_write_tokens > 0) &&
              ` · 缓存读 ${formatTokens(call.usage.cache_read_tokens)} / 写 ${formatTokens(call.usage.cache_write_tokens)}`}
          </span>
        </Row>
        <Row label="耗时">
          <span className="num">
            首字 {formatMs(call.ttft_ms)} · 总计 {formatMs(call.duration_ms)}
          </span>
        </Row>
      </dl>
      {snapshot && (
        <section className="call-detail-section" aria-label="计价明细">
          <h3>计价明细</h3>
          <PriceSnapshotView snapshot={snapshot} />
        </section>
      )}
      <section className="call-detail-section" aria-label="尝试时间线">
        <h3>尝试 {call.attempts.length} 次</h3>
        <AttemptTimeline attempts={call.attempts} call={call} />
      </section>
    </div>
  )
}

/** 调用详情抽屉：query 由调用方提供（用户视图用 useCall，管理员视图可换成自己的查询）。 */
export function CallDetailDrawer({
  open,
  onClose,
  query,
}: {
  open: boolean
  onClose: () => void
  query: QueryLike<CallDetail>
}) {
  return (
    <Drawer onClose={onClose} open={open} title="调用详情">
      <QueryBoundary errorFallback="调用详情加载失败" query={query}>
        {(call) => <CallDetailView call={call} />}
      </QueryBoundary>
    </Drawer>
  )
}
