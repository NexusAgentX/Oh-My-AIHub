import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { AuthorizedValidationAttempt, Channel, ChannelOffer } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState } from '../ui/FormControls'
import { ChannelStateBadge, ConfirmDialog, formatDate, PricePair, protocolLabels, ratingText, TierCountBadge, TierPriceList } from './presentation'
import { formatPoints, formatRate } from '../gateway/presentation'
import { createLatestRequestGate } from './requestGate'

type PendingAction =
  | { kind: 'validate'; offer: ChannelOffer }
  | { kind: 'delete-offer'; offer: ChannelOffer }
  | { kind: 'publish' | 'pause' | 'delete-channel' | 'revoke-credential' }

export function ChannelDetailPage() {
  const { channelID = '' } = useParams()
  const navigate = useNavigate()
  const [channel, setChannel] = useState<Channel | null>(null)
  const [history, setHistory] = useState<AuthorizedValidationAttempt[]>([])
  const [historyOffer, setHistoryOffer] = useState<ChannelOffer | null>(null)
  const [pending, setPending] = useState<PendingAction | null>(null)
  const [costConfirmed, setCostConfirmed] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const loadGate = useRef(createLatestRequestGate())
  const historyGate = useRef(createLatestRequestGate())
  const actionGate = useRef(createLatestRequestGate())

  const load = useCallback(async (clear = false) => {
    const ticket = loadGate.current.begin()
    if (clear) {
      setLoading(true)
      setChannel(null)
    }
    setError('')
    try {
      const loaded = await api.channel(channelID)
      if (loadGate.current.isCurrent(ticket)) setChannel(loaded)
    } catch (caught) {
      if (loadGate.current.isCurrent(ticket)) {
        setError(caught instanceof ApiError ? caught.message : '渠道加载失败')
      }
    } finally {
      if (loadGate.current.isCurrent(ticket)) setLoading(false)
    }
  }, [channelID])

  useEffect(() => {
    historyGate.current.invalidate()
    actionGate.current.invalidate()
    setHistory([])
    setHistoryOffer(null)
    setPending(null)
    setCostConfirmed(false)
    setBusy(false)
    setMessage('')
    void load(true)
    return () => {
      loadGate.current.invalidate()
      historyGate.current.invalidate()
      actionGate.current.invalidate()
    }
  }, [load])

  const activeOffers = useMemo(
    () => channel?.offers.filter((offer) => offer.status !== 'deleted') ?? [],
    [channel],
  )
  const eligibleOffers = useMemo(
    () => activeOffers.filter((offer) => offer.eligible),
    [activeOffers],
  )

  const showHistory = async (offer: ChannelOffer) => {
    const ticket = historyGate.current.begin()
    setError('')
    try {
      const attempts = await api.channelValidationAttempts(offer.id)
      if (!historyGate.current.isCurrent(ticket)) return
      setHistory(attempts)
      setHistoryOffer(offer)
    } catch (caught) {
      if (historyGate.current.isCurrent(ticket)) {
        setError(caught instanceof ApiError ? caught.message : '验证记录加载失败')
      }
    }
  }

  const toggleOffer = async (offer: ChannelOffer) => {
    const ticket = actionGate.current.begin()
    setBusy(true)
    setError('')
    try {
      await api.setChannelOfferStatus(offer.id, offer.status === 'active' ? 'disable' : 'resume', offer.version ?? 0)
      if (!actionGate.current.isCurrent(ticket)) return
      await load(false)
    } catch (caught) {
      if (actionGate.current.isCurrent(ticket)) {
        setError(caught instanceof ApiError ? caught.message : '报价状态更新失败')
      }
    } finally {
      if (actionGate.current.isCurrent(ticket)) setBusy(false)
    }
  }

  const confirm = async () => {
    if (!pending || !channel) return
    const ticket = actionGate.current.begin()
    const targetChannel = channel
    setBusy(true)
    setError('')
    setMessage('')
    try {
      if (pending.kind === 'validate') {
        const result = await api.validateChannelOffer(pending.offer.id)
        if (!actionGate.current.isCurrent(ticket)) return
        setMessage(result.status === 'passed' ? '验证通过' : '验证失败')
        await showHistory(pending.offer)
      } else if (pending.kind === 'delete-offer') {
        await api.deleteChannelOffer(pending.offer.id, pending.offer.version ?? 0)
      } else if (pending.kind === 'revoke-credential') {
        await api.revokeChannelCredential(targetChannel.id, targetChannel.version)
      } else if (pending.kind === 'delete-channel') {
        await api.deleteChannel(targetChannel.id, targetChannel.version)
        if (!actionGate.current.isCurrent(ticket)) return
        navigate('/channels', { replace: true })
        return
      } else {
        await api.setChannelStatus(targetChannel.id, pending.kind, targetChannel.version)
      }
      if (!actionGate.current.isCurrent(ticket)) return
      setPending(null)
      setCostConfirmed(false)
      await load(false)
    } catch (caught) {
      if (actionGate.current.isCurrent(ticket)) {
        setError(caught instanceof ApiError ? caught.message : '操作失败')
      }
    } finally {
      if (actionGate.current.isCurrent(ticket)) setBusy(false)
    }
  }

  if (loading) return <AppShell><LoadingState /></AppShell>
  if (!channel || channel.id !== channelID) return <AppShell><InlineError>{error || '渠道不存在'}</InlineError></AppShell>

  const pendingTitle = pending?.kind === 'validate' ? '验证报价'
    : pending?.kind === 'delete-offer' ? '删除协议报价'
      : pending?.kind === 'revoke-credential' ? '撤销平台凭据'
        : pending?.kind === 'delete-channel' ? '删除渠道'
          : pending?.kind === 'pause' ? '暂停渠道' : '发布渠道'

  return (
    <AppShell>
      <Link className="back-link" to="/channels">← 我的渠道</Link>
      <header className="page-heading channel-detail-heading">
        <div><h1>{channel.display_name}</h1><ChannelStateBadge status={channel.status} /></div>
        {channel.status !== 'deleted' && <Link className="button button-secondary" to={`/channels/${channel.id}/settings`}>编辑配置</Link>}
      </header>
      <InlineError>{error}</InlineError>
      {message && <div aria-live="polite" className="success-message">{message}</div>}
      <section aria-label="渠道概况" className="metric-grid">
        <article className="metric-card"><span>协议报价</span><strong>{activeOffers.length}</strong><small>未删除</small></article>
        <article className="metric-card metric-card-accent"><span>当前可用</span><strong>{eligibleOffers.length}</strong><small>可加入模型池</small></article>
        <article className="metric-card"><span>评分</span><strong>{channel.average_rating ?? '—'}</strong><small>{ratingText(channel.average_rating, channel.rating_count)}</small></article>
        <article className="metric-card"><span>凭据</span><strong>{channel.credential_configured ? '已配置' : '未配置'}</strong><small>版本 {channel.credential_version}</small></article>
      </section>

      {channel.status === 'published' && eligibleOffers.length === 0 && <div className="availability-message" role="status">暂无可用报价</div>}

      <section className="panel channel-overview-panel">
        <header className="panel-heading"><h2>渠道概况</h2><div className="channel-action-row">
          {(channel.status === 'draft' || channel.status === 'paused') && <Button onClick={() => setPending({ kind: 'publish' })}>发布</Button>}
          {channel.status === 'published' && <Button onClick={() => setPending({ kind: 'pause' })} variant="secondary">暂停</Button>}
          {channel.credential_configured && channel.status !== 'deleted' && <Button onClick={() => setPending({ kind: 'revoke-credential' })} variant="secondary">撤销凭据</Button>}
          {channel.status !== 'deleted' && <Button onClick={() => setPending({ kind: 'delete-channel' })} variant="danger">删除渠道</Button>}
        </div></header>
        <dl className="detail-list">
          <div><dt>Base URL</dt><dd className="break-value">{channel.base_url}</dd></div>
          <div><dt>最近更新</dt><dd>{formatDate(channel.updated_at)}</dd></div>
          <div><dt>渠道版本</dt><dd>{channel.version}</dd></div>
        </dl>
      </section>

      <section className="panel table-panel channel-offers-panel">
        <header className="table-toolbar"><h2>模型协议报价</h2></header>
        {activeOffers.length === 0 ? <div className="empty-state">没有协议报价</div> : <>
          <div className="desktop-table-wrap">
            <table className="data-table channel-offer-table">
              <thead><tr><th scope="col">模型 / 协议</th><th scope="col">倍率</th><th scope="col">输入 / 输出</th><th scope="col">缓存写 / 读</th><th scope="col">调用质量</th><th scope="col">收入</th><th scope="col">验证</th><th scope="col"><span className="visually-hidden">操作</span></th></tr></thead>
              <tbody>{activeOffers.map((offer) => <tr key={offer.id}>
                <td><strong>{offer.model_name}</strong><small>{protocolLabels[offer.protocol]} · {offer.upstream_model_id}</small></td>
                <td>{offer.multiplier}×</td>
                <td><span className="price-with-tiers"><PricePair first={offer.input_price} second={offer.output_price} /><TierCountBadge tiers={offer.price_tiers} /></span></td>
                <td><span className="price-with-tiers"><PricePair first={offer.cache_write_price} second={offer.cache_read_price} /><TierCountBadge tiers={offer.price_tiers} /></span></td>
                <td>{offer.call_success_rate === null || offer.call_success_rate === undefined ? '—' : <><strong>{formatRate(offer.call_success_rate)}</strong><small>{offer.call_count ?? 0} 次 · {offer.ttft_milliseconds ?? '—'} ms · {offer.tokens_per_second ?? '—'} tok/s</small></>}</td>
                <td>{offer.provider_income ? `${formatPoints(offer.provider_income)} 积分` : '—'}</td>
                <td>{offer.latest_validation ? <ChannelStateBadge status={offer.latest_validation.status} /> : '待验证'}<small>{offer.eligible ? '当前可用' : eligibilityReason(offer.ineligible_reason)}</small><small>{formatDate(offer.latest_validation?.completed_at)}</small></td>
                <td className="table-action"><div className="table-action-group">
                  <Button disabled={busy} onClick={() => { setCostConfirmed(false); setPending({ kind: 'validate', offer }) }} variant="secondary">验证</Button>
                  <Button onClick={() => void showHistory(offer)} variant="quiet">记录</Button>
                  <Button disabled={busy} onClick={() => void toggleOffer(offer)} variant="quiet">{offer.status === 'active' ? '停用' : '启用'}</Button>
                  <Button onClick={() => setPending({ kind: 'delete-offer', offer })} variant="quiet">删除</Button>
                </div></td>
              </tr>)}</tbody>
            </table>
          </div>
          <div className="mobile-card-list">{activeOffers.map((offer) => <article className="mobile-data-card" key={offer.id}>
            <header><div><strong>{offer.model_name}</strong><span>{protocolLabels[offer.protocol]}</span></div>{offer.latest_validation ? <ChannelStateBadge status={offer.latest_validation.status} /> : <span>待验证</span>}</header>
            <dl><div><dt>资格</dt><dd>{offer.eligible ? '当前可用' : eligibilityReason(offer.ineligible_reason)}</dd></div><div><dt>倍率</dt><dd>{offer.multiplier}×</dd></div><div><dt>输入 / 输出</dt><dd><PricePair first={offer.input_price} second={offer.output_price} /></dd></div><div><dt>缓存写 / 读</dt><dd><PricePair first={offer.cache_write_price} second={offer.cache_read_price} /></dd></div><div><dt>成功率 / 调用</dt><dd>{offer.call_success_rate === null || offer.call_success_rate === undefined ? '—' : `${formatRate(offer.call_success_rate)} / ${offer.call_count ?? 0}`}</dd></div><div><dt>收入</dt><dd>{offer.provider_income ? `${formatPoints(offer.provider_income)} 积分` : '—'}</dd></div></dl>
            <TierPriceList tiers={offer.price_tiers} />
            <div className="mobile-card-actions"><Button disabled={busy} onClick={() => { setCostConfirmed(false); setPending({ kind: 'validate', offer }) }} variant="secondary">验证</Button><Button onClick={() => void showHistory(offer)} variant="secondary">记录</Button><Button disabled={busy} onClick={() => void toggleOffer(offer)} variant="secondary">{offer.status === 'active' ? '停用' : '启用'}</Button><Button onClick={() => setPending({ kind: 'delete-offer', offer })} variant="danger">删除</Button></div>
          </article>)}</div>
        </>}
      </section>

      {historyOffer && <section className="panel validation-history-panel">
        <header className="panel-heading"><h2>验证记录 · {historyOffer.model_name}</h2><Button onClick={() => setHistoryOffer(null)} variant="quiet">关闭</Button></header>
        {history.length === 0 ? <div className="empty-state">暂无记录</div> : <div className="validation-history-list">{history.map((attempt) => <article key={attempt.id}>
          <header><ChannelStateBadge status={attempt.status} /><span>{formatDate(attempt.completed_at ?? attempt.started_at)}</span><span>{attempt.http_status ? `HTTP ${attempt.http_status}` : attempt.error_category || '—'}</span></header>
          {attempt.raw_error && <pre>{attempt.raw_error}</pre>}
        </article>)}</div>}
      </section>}

      <ConfirmDialog
        busy={busy}
        confirmDisabled={pending?.kind === 'validate' && !costConfirmed}
        confirmLabel={pending?.kind === 'validate' ? '开始验证' : '确认'}
        danger={pending?.kind === 'delete-channel' || pending?.kind === 'delete-offer' || pending?.kind === 'revoke-credential'}
        description={pending?.kind === 'validate' ? '将向上游发送最小请求。' : undefined}
        onCancel={() => { setPending(null); setCostConfirmed(false) }}
        onConfirm={() => void confirm()}
        open={Boolean(pending)}
        title={pendingTitle}
      >
        {pending?.kind === 'validate' && <label className="checkbox-control validation-cost-confirm"><input checked={costConfirmed} onChange={(event) => setCostConfirmed(event.target.checked)} type="checkbox" /><span>我确认可能产生少量上游费用</span></label>}
      </ConfirmDialog>
    </AppShell>
  )
}

function eligibilityReason(reason: string) {
  switch (reason) {
    case 'credential_unavailable': return '凭据不可用'
    case 'model_inactive': return '模型已停用'
    case 'offer_inactive': return '报价已停用'
    case 'validation_required': return '需要重新验证'
    case 'channel_unpublished': return '渠道未发布'
    case 'owner_inactive': return '账户已停用'
    case 'owner_password_change_required': return '账户需先改密'
    case 'price_unrepresentable': return '价格不可用'
    default: return '当前不可用'
  }
}
