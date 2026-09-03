import { useCallback, useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { GatewayCall } from '../api/contracts'
import { formatDate, protocolLabels } from '../channels/presentation'
import { AppShell } from '../layouts/AppShell'
import { InlineError, LoadingState } from '../ui/FormControls'
import { formatNullableMetric, GatewayStatusBadge, formatPoints, shortID, totalTokens } from './presentation'

export function CallDetailPage() {
  const { callID = '' } = useParams()
  const [call, setCall] = useState<GatewayCall | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setCall(await api.gatewayCall(callID))
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '调用详情加载失败')
    } finally {
      setLoading(false)
    }
  }, [callID])

  useEffect(() => { void load() }, [load])

  if (loading) return <AppShell><LoadingState /></AppShell>
  if (!call) return <AppShell><InlineError>{error || '调用不存在'}</InlineError></AppShell>

  return (
    <AppShell>
      <Link className="back-link" to="/calls">← 调用记录</Link>
      <header className="page-heading channel-detail-heading">
        <div><h1>{call.model_id || '调用详情'}</h1><GatewayStatusBadge status={call.status} /></div>
      </header>
      <InlineError>{error}</InlineError>
      <section className="metric-grid">
        <article className="metric-card"><span>提供者费用</span><strong>{formatPoints(call.provider_charge)}</strong><small>积分</small></article>
        <article className="metric-card"><span>平台手续费</span><strong>{formatPoints(call.platform_fee)}</strong><small>积分</small></article>
        <article className="metric-card"><span>Tokens</span><strong>{totalTokens(call)}</strong><small>四类用量合计</small></article>
        <article className="metric-card"><span>上游尝试</span><strong>{call.attempt_count}</strong><small>{call.final_channel_name || '未命中渠道'}</small></article>
      </section>
      <section className="gateway-detail-grid">
        <article className="panel">
          <header className="panel-heading"><h2>调用快照</h2></header>
          <dl className="detail-list">
            <div><dt>调用 ID</dt><dd className="mono-value">{shortID(call.id)}</dd></div>
            <div><dt>API 格式</dt><dd>{call.protocol ? protocolLabels[call.protocol] : '—'}</dd></div>
            <div><dt>Key</dt><dd className="mono-value">{call.key_prefix ? `${call.key_prefix}… · 第 ${call.key_generation} 代` : '—'}</dd></div>
            <div><dt>池版本</dt><dd>{call.pool_version || '—'}</dd></div>
            <div><dt>候选数</dt><dd>{call.candidate_count}</dd></div>
            <div><dt>创建时间</dt><dd>{formatDate(call.created_at)}</dd></div>
          </dl>
        </article>
        <article className="panel">
          <header className="panel-heading"><h2>结算结果</h2></header>
          <dl className="detail-list">
            <div><dt>预授权上限</dt><dd>{formatPoints(call.preauthorized)} 积分</dd></div>
            <div><dt>计费档位</dt><dd>{call.settled_price_tier_seq > 0 ? `条件档 #${call.settled_price_tier_seq}` : '默认档'}</dd></div>
            <div><dt>最终渠道</dt><dd>{call.final_channel_name || '—'}</dd></div>
            <div><dt>HTTP 状态</dt><dd>{call.final_http_status || '—'}</dd></div>
            <div><dt>完成原因</dt><dd>{call.completion_reason || call.decision_code || '—'}</dd></div>
            <div><dt>完成时间</dt><dd>{formatDate(call.completed_at)}</dd></div>
          </dl>
        </article>
      </section>
      <section className="panel table-panel call-attempt-panel">
        <header className="table-toolbar"><h2>上游尝试</h2><span className="count-badge">{call.attempts.length}</span></header>
        {call.attempts.length === 0 ? <div className="empty-state">没有访问上游</div> : <>
          <div className="desktop-table-wrap"><table className="data-table"><thead><tr><th scope="col">顺序 / 渠道</th><th scope="col">状态</th><th scope="col">HTTP</th><th scope="col">TTFT</th><th scope="col">耗时</th><th scope="col">TPS</th><th scope="col">错误</th></tr></thead><tbody>{call.attempts.map((attempt) => <tr key={attempt.id}>
            <td><strong>#{attempt.sequence} {attempt.channel_name}</strong><small className="mono-value">{shortID(attempt.offer_id)}</small></td>
            <td><GatewayStatusBadge status={attempt.status} /></td><td>{attempt.http_status || '—'}</td><td>{formatNullableMetric(attempt.ttft_milliseconds)} ms</td><td>{formatNullableMetric(attempt.duration_milliseconds)} ms</td><td>{formatNullableMetric(attempt.tokens_per_second)}</td><td><strong>{attempt.error_code || '—'}</strong>{attempt.raw_error && <small className="raw-error-value">{attempt.raw_error}</small>}</td>
          </tr>)}</tbody></table></div>
          <div className="mobile-card-list">{call.attempts.map((attempt) => <article className="mobile-data-card" key={attempt.id}><header><div><strong>#{attempt.sequence} {attempt.channel_name}</strong><span className="mono-value">{shortID(attempt.offer_id)}</span></div><GatewayStatusBadge status={attempt.status} /></header><dl><div><dt>HTTP</dt><dd>{attempt.http_status || '—'}</dd></div><div><dt>TTFT / 耗时</dt><dd>{formatNullableMetric(attempt.ttft_milliseconds)} / {formatNullableMetric(attempt.duration_milliseconds)} ms</dd></div><div><dt>错误码</dt><dd>{attempt.error_code || '—'}</dd></div>{attempt.raw_error && <div><dt>错误消息</dt><dd className="raw-error-value">{attempt.raw_error}</dd></div>}</dl></article>)}</div>
        </>}
      </section>
    </AppShell>
  )
}
