import { Link, useLocation } from 'react-router-dom'
import { CallNetwork } from './CallNetwork'
import { CallDiagnostics } from './CallDiagnostics'
import { PriceSnapshotView } from '../admin/transactions'
import type { CallDetail } from '../api/types'
import { formatMs, formatPoints, formatTokens } from '../money/format'
import { MultiplierText } from '../money/MultiplierText'
import { formatDateTime } from '../money/time'
import { ButtonLink, CopyButton, Drawer, QueryBoundary } from '../ui'
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

export function CallResponseMetrics({ call }: { call: CallDetail }) {
  const measured = call.attempts?.some(attempt => attempt.metrics_version === 2)
  return (
    <>
      {' '}
      <Row label={measured || call.output_tokens_per_second == null ? '有效输出速率' : call.stream ? '输出速度（历史口径）' : '端到端平均速度（历史口径）'}>
        {call.output_tokens_per_second == null ? '未知' : `${call.output_tokens_per_second.toFixed(1)} tokens/s`}
      </Row>
      {call.stream && call.inter_token_p50_ms !== null && (
        <Row label="数据帧间隔 p50 / p95">
          {formatMs(call.inter_token_p50_ms)} / {formatMs(call.inter_token_p95_ms)}
        </Row>
      )}
    </>
  )
}

/** 调用详情内容：请求信息、计价、尝试时间线。抽屉与独立页面共用。 */
export function CallDetailView({ call, onRefresh }: { call: CallDetail; onRefresh?: () => unknown }) {
  const snapshot = call.price_snapshot
  const admin = useLocation().pathname.startsWith('/admin/')
  return (
    <div className="call-detail">
      <div className="call-detail-id">
        <code className="mono">{call.id}</code>
        <CopyButton iconOnly label="复制请求 ID" value={call.id} />
      </div>
      <dl className="call-detail-grid">
        <Row label="结果">
          <OutcomeBadge outcome={call.outcome} />
        </Row>
        <Row label="开始">{formatDateTime(call.created_at)}</Row>
        {call.completed_at && <Row label="完成">{formatDateTime(call.completed_at)}</Row>}
        {call.client_user_agent && <Row label="客户端">{call.client_user_agent}</Row>}
        {call.upstream_response_id && (
          <Row label="上游响应 ID">
            <code>{call.upstream_response_id}</code>
            <CopyButton iconOnly label="复制上游响应 ID" value={call.upstream_response_id} />
          </Row>
        )}
        {call.response_bytes !== null && <Row label="已发送">{call.response_bytes.toLocaleString()} bytes</Row>}
        {call.ledger_transaction_id && (
          <Row label="账本交易">
            <Link
              to={`${admin ? '/admin/points?tab=transactions' : '/points?tab=bill'}&transaction=${call.ledger_transaction_id}`}
            >
              {call.ledger_transaction_id}
            </Link>
          </Row>
        )}
        <CallResponseMetrics call={call} />
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
          {snapshot?.group_quote ? <span>分组报价 · 修订 {snapshot.group_quote.group_revision}</span> : snapshot ? (
            <>
              {snapshot.tier?.name ?? '基准价'} ·{' '}
              <MultiplierText
                effective={snapshot.effective_multiplier}
                fxDiscount={snapshot.fx_discount}
                multiplier={snapshot.multiplier}
              />
              {snapshot.currency === 'USD' && snapshot.usd_cny_rate && (
                <small className="muted"> · 美元模型，汇率 {snapshot.usd_cny_rate}</small>
              )}
            </>
          ) : (
            '—'
          )}
        </Row>
        <Row label="实际扣费">
          <span className="num">
            {formatPoints(call.charged)}
            <small className="muted">
              {' '}
              · 计价 {formatPoints(call.cost)} + 手续费 {formatPoints(call.fee)}
            </small>
          </span>
        </Row>
        {call.diagnostics && (
          <Row label="入账">{call.ledger_transaction_id ? '已入账' : call.completed_at ? '未入账' : '处理中'}</Row>
        )}
        <Row label="tokens">
          <span className="num">
            输入 {formatTokens(call.usage.input_tokens)} · 输出 {formatTokens(call.usage.output_tokens)}
            {(call.usage.cache_read_tokens > 0 || call.usage.cache_write_tokens > 0) &&
              ` · 缓存读 ${formatTokens(call.usage.cache_read_tokens)} / 写 ${formatTokens(call.usage.cache_write_tokens)}`}
          </span>
        </Row>
        <Row label="耗时">
          <span className="num">
            首响应 {formatMs(call.ttft_ms)} · 总计 {formatMs(call.duration_ms)}
          </span>
        </Row>
      </dl>
      {call.diagnostics && <CallNetwork call={call} onRefresh={onRefresh} />}
      {call.diagnostics && <CallDiagnostics data={call.diagnostics} />}
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
  const location = useLocation()
  const base = location.pathname.startsWith('/admin/') ? '/admin/calls' : '/calls'
  return (
    <Drawer onClose={onClose} open={open} title="调用详情">
      {query.data && <ButtonLink variant="quiet" to={`${base}/${query.data.id}`} state={{ from: { to: `${location.pathname}${location.search}`, label: location.pathname === '/home' ? '首页' : '调用列表' } }}>打开完整页面</ButtonLink>}
      <QueryBoundary errorFallback="调用详情加载失败" query={query}>
        {(call) => <CallDetailView call={call} onRefresh={query.refetch} />}
      </QueryBoundary>
    </Drawer>
  )
}
