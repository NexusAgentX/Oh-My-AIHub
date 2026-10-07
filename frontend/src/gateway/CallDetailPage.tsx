import { Link, useParams } from 'react-router-dom'
import {
  Card,
  CountBadge,
  DataTable,
  EmptyState,
  Metric,
  MetricGrid,
  PageHeader,
  QueryBoundary,
  type Column,
} from '../ui'
import type { GatewayAttempt } from '../api/contracts'
import {
  GatewayStatusBadge,
  formatDate,
  formatNullableMetric,
  formatPoints,
  protocolLabels,
  shortID,
  totalTokens,
} from './presentation'
import { useGatewayCallQuery } from './queries'

const attemptColumns: Column<GatewayAttempt>[] = [
  {
    key: 'channel',
    header: '顺序 / 渠道',
    primary: true,
    cell: (attempt) => (
      <>
        <strong>
          #{attempt.sequence} {attempt.channel_name}
        </strong>
        <small className="mono">{shortID(attempt.offer_id)}</small>
      </>
    ),
  },
  { key: 'status', header: '状态', cell: (attempt) => <GatewayStatusBadge status={attempt.status} /> },
  { key: 'http', header: 'HTTP', numeric: true, cell: (attempt) => attempt.http_status || '—' },
  {
    key: 'ttft',
    header: 'TTFT',
    numeric: true,
    cell: (attempt) => `${formatNullableMetric(attempt.ttft_milliseconds)} ms`,
  },
  {
    key: 'duration',
    header: '耗时',
    numeric: true,
    cell: (attempt) => `${formatNullableMetric(attempt.duration_milliseconds)} ms`,
  },
  { key: 'tps', header: 'TPS', numeric: true, cell: (attempt) => formatNullableMetric(attempt.tokens_per_second) },
  {
    key: 'error',
    header: '错误',
    cell: (attempt) => (
      <>
        <strong>{attempt.error_code || '—'}</strong>
        {attempt.raw_error && <small className="raw-error">{attempt.raw_error}</small>}
      </>
    ),
  },
]

export function CallDetailPage() {
  const { callID = '' } = useParams()
  const query = useGatewayCallQuery(callID)

  return (
    <QueryBoundary errorFallback="调用详情加载失败" query={query}>
      {(call) => (
        <>
          <PageHeader
            back={
              <Link className="back-link" to="/calls">
                ← 调用记录
              </Link>
            }
            actions={<GatewayStatusBadge status={call.status} />}
            title={call.model_id || '调用详情'}
          />
          <MetricGrid label="结算概览">
            <Metric hint="积分" label="提供者费用" value={formatPoints(call.provider_charge)} />
            <Metric hint="积分" label="平台手续费" value={formatPoints(call.platform_fee)} />
            <Metric hint="四类用量合计" label="Tokens" value={totalTokens(call)} />
            <Metric hint={call.final_channel_name || '未命中渠道'} label="上游尝试" value={call.attempt_count} />
          </MetricGrid>
          <div className="detail-grid">
            <Card flush title="调用快照">
              <dl className="detail-list">
                <div><dt>调用 ID</dt><dd className="mono">{shortID(call.id)}</dd></div>
                <div><dt>API 格式</dt><dd>{call.protocol ? protocolLabels[call.protocol] : '—'}</dd></div>
                <div>
                  <dt>Key</dt>
                  <dd className="mono">
                    {call.key_prefix ? `${call.key_prefix}… · 第 ${call.key_generation} 代` : '—'}
                  </dd>
                </div>
                <div><dt>路由版本</dt><dd>{call.pool_version || '—'}</dd></div>
                <div><dt>候选渠道</dt><dd>{call.candidate_count}</dd></div>
                <div><dt>创建时间</dt><dd>{formatDate(call.created_at)}</dd></div>
              </dl>
            </Card>
            <Card flush title="结算结果">
              <dl className="detail-list">
                <div><dt>预授权上限</dt><dd>{formatPoints(call.preauthorized)} 积分</dd></div>
                <div>
                  <dt>计费档位</dt>
                  <dd>{call.settled_price_tier_seq > 0 ? `条件档 #${call.settled_price_tier_seq}` : '默认档'}</dd>
                </div>
                <div><dt>最终渠道</dt><dd>{call.final_channel_name || '—'}</dd></div>
                <div><dt>HTTP 状态</dt><dd>{call.final_http_status || '—'}</dd></div>
                <div><dt>完成原因</dt><dd>{call.completion_reason || call.decision_code || '—'}</dd></div>
                <div><dt>完成时间</dt><dd>{formatDate(call.completed_at)}</dd></div>
              </dl>
            </Card>
          </div>
          <Card
            actions={<CountBadge>{call.attempts.length}</CountBadge>}
            className="call-attempts"
            flush
            title="上游尝试"
          >
            <DataTable
              caption="上游尝试"
              columns={attemptColumns}
              empty={<EmptyState title="没有访问上游" />}
              rowKey={(attempt) => attempt.id}
              rows={call.attempts}
            />
          </Card>
        </>
      )}
    </QueryBoundary>
  )
}
