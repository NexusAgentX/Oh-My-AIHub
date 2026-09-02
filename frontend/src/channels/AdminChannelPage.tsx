import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { AdminChannel, AdminChannelOffer, AuthorizedValidationAttempt } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState } from '../ui/FormControls'
import { ChannelStateBadge, ConfirmDialog, formatDate, protocolLabels, ratingText } from './presentation'
import { createLatestRequestGate } from './requestGate'

type AdminAction = { kind: 'validate'; offer: AdminChannelOffer } | { kind: 'pause' | 'delete' }

export function AdminChannelPage() {
  const { channelID = '' } = useParams()
  const navigate = useNavigate()
  const [channel, setChannel] = useState<AdminChannel | null>(null)
  const [action, setAction] = useState<AdminAction | null>(null)
  const [reason, setReason] = useState('')
  const [costConfirmed, setCostConfirmed] = useState(false)
  const [historyOffer, setHistoryOffer] = useState<AdminChannelOffer | null>(null)
  const [history, setHistory] = useState<AuthorizedValidationAttempt[]>([])
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
      const loaded = await api.adminChannel(channelID)
      if (loadGate.current.isCurrent(ticket)) setChannel(loaded)
    } catch (caught) {
      if (loadGate.current.isCurrent(ticket)) {
        setError(caught instanceof ApiError ? caught.message : '渠道治理详情加载失败')
      }
    } finally {
      if (loadGate.current.isCurrent(ticket)) setLoading(false)
    }
  }, [channelID])

  useEffect(() => {
    historyGate.current.invalidate()
    actionGate.current.invalidate()
    setAction(null)
    setReason('')
    setCostConfirmed(false)
    setHistoryOffer(null)
    setHistory([])
    setBusy(false)
    setMessage('')
    void load(true)
    return () => {
      loadGate.current.invalidate()
      historyGate.current.invalidate()
      actionGate.current.invalidate()
    }
  }, [load])

  const showHistory = async (offer: AdminChannelOffer) => {
    const ticket = historyGate.current.begin()
    setError('')
    try {
      const attempts = await api.channelValidationAttempts(offer.id, true)
      if (!historyGate.current.isCurrent(ticket)) return
      setHistory(attempts)
      setHistoryOffer(offer)
    } catch (caught) {
      if (historyGate.current.isCurrent(ticket)) {
        setError(caught instanceof ApiError ? caught.message : '验证记录加载失败')
      }
    }
  }

  const confirm = async () => {
    if (!action || !channel) return
    const ticket = actionGate.current.begin()
    const targetChannel = channel
    setBusy(true)
    setError('')
    setMessage('')
    try {
      if (action.kind === 'validate') {
        const result = await api.validateChannelOffer(action.offer.id, true)
        if (!actionGate.current.isCurrent(ticket)) return
        setMessage(result.status === 'passed' ? '验证通过' : '验证失败')
        await showHistory(action.offer)
      } else {
        await api.adminSetChannelStatus(targetChannel.id, action.kind, targetChannel.version, reason.trim())
        if (!actionGate.current.isCurrent(ticket)) return
        if (action.kind === 'delete') {
          navigate('/admin/channels', { replace: true })
          return
        }
      }
      if (!actionGate.current.isCurrent(ticket)) return
      setAction(null)
      setReason('')
      setCostConfirmed(false)
      await load(false)
    } catch (caught) {
      if (actionGate.current.isCurrent(ticket)) {
        setError(caught instanceof ApiError ? caught.message : '治理操作失败')
      }
    } finally {
      if (actionGate.current.isCurrent(ticket)) setBusy(false)
    }
  }

  if (loading) return <AppShell admin><LoadingState /></AppShell>
  if (!channel || channel.id !== channelID) return <AppShell admin><InlineError>{error || '渠道不存在'}</InlineError></AppShell>

  return <AppShell admin>
    <Link className="back-link" to="/admin/channels">← 渠道治理</Link>
    <header className="page-heading channel-detail-heading"><div><h1>{channel.display_name}</h1><ChannelStateBadge status={channel.status} /></div></header>
    <InlineError>{error}</InlineError>
    {message && <div aria-live="polite" className="success-message">{message}</div>}
    <section className="panel admin-channel-summary"><header className="panel-heading"><h2>渠道概况</h2><div className="channel-action-row">{channel.status === 'published' && <Button onClick={() => setAction({ kind: 'pause' })} variant="secondary">暂停渠道</Button>} {channel.status !== 'deleted' && <Button onClick={() => setAction({ kind: 'delete' })} variant="danger">删除渠道</Button>}</div></header><dl className="detail-list"><div><dt>共享者</dt><dd>{channel.owner_display_name}</dd></div><div><dt>凭据</dt><dd>{channel.credential_configured ? `已配置 · v${channel.credential_version}` : '未配置'}</dd></div><div><dt>评分</dt><dd>{ratingText(channel.average_rating, channel.rating_count)}</dd></div><div><dt>最近更新</dt><dd>{formatDate(channel.updated_at)}</dd></div></dl></section>
    <section className="panel table-panel admin-channel-offers"><header className="table-toolbar"><h2>协议报价</h2></header>{channel.offers.length === 0 ? <div className="empty-state">没有报价</div> : <><div className="desktop-table-wrap"><table className="data-table"><thead><tr><th scope="col">模型 / 协议</th><th scope="col">倍率</th><th scope="col">状态</th><th scope="col">验证</th><th scope="col"><span className="visually-hidden">操作</span></th></tr></thead><tbody>{channel.offers.map((offer) => <tr key={offer.id}><td><strong>{offer.model_name}</strong><small>{protocolLabels[offer.protocol]}</small></td><td>{offer.multiplier}×</td><td><ChannelStateBadge status={offer.status} /></td><td>{offer.latest_validation ? <ChannelStateBadge status={offer.latest_validation.status} /> : '待验证'}<small>{formatDate(offer.latest_validation?.completed_at)}</small></td><td className="table-action"><div className="table-action-group"><Button onClick={() => { setCostConfirmed(false); setAction({ kind: 'validate', offer }) }} variant="secondary">重验</Button><Button onClick={() => void showHistory(offer)} variant="quiet">记录</Button></div></td></tr>)}</tbody></table></div><div className="mobile-card-list">{channel.offers.map((offer) => <article className="mobile-data-card" key={offer.id}><header><div><strong>{offer.model_name}</strong><span>{protocolLabels[offer.protocol]}</span></div><ChannelStateBadge status={offer.status} /></header><dl><div><dt>倍率</dt><dd>{offer.multiplier}×</dd></div><div><dt>验证</dt><dd>{offer.latest_validation?.status ?? '待验证'}</dd></div></dl><div className="mobile-card-actions"><Button onClick={() => { setCostConfirmed(false); setAction({ kind: 'validate', offer }) }} variant="secondary">重验</Button><Button onClick={() => void showHistory(offer)} variant="secondary">记录</Button></div></article>)}</div></>}</section>
    {historyOffer && <section className="panel validation-history-panel"><header className="panel-heading"><h2>验证记录 · {historyOffer.model_name}</h2><Button onClick={() => setHistoryOffer(null)} variant="quiet">关闭</Button></header>{history.length === 0 ? <div className="empty-state">暂无记录</div> : <div className="validation-history-list">{history.map((attempt) => <article key={attempt.id}><header><ChannelStateBadge status={attempt.status} /><span>{formatDate(attempt.completed_at ?? attempt.started_at)}</span><span>{attempt.http_status ? `HTTP ${attempt.http_status}` : attempt.error_category || '—'}</span><span>操作者 {attempt.actor_account_id}</span></header>{attempt.raw_error && <pre>{attempt.raw_error}</pre>}</article>)}</div>}</section>}
    <ConfirmDialog
      busy={busy}
      confirmDisabled={action?.kind === 'validate' ? !costConfirmed : !reason.trim()}
      confirmLabel={action?.kind === 'validate' ? '开始验证' : action?.kind === 'delete' ? '删除渠道' : '暂停渠道'}
      danger={action?.kind === 'delete'}
      description={action?.kind === 'validate' ? '将向上游发送最小请求。' : undefined}
      onCancel={() => { setAction(null); setReason(''); setCostConfirmed(false) }}
      onConfirm={() => void confirm()}
      open={Boolean(action)}
      title={action?.kind === 'validate' ? '管理员重验' : action?.kind === 'delete' ? '删除渠道' : '暂停渠道'}
    >
      {action?.kind === 'validate' ? <label className="checkbox-control validation-cost-confirm"><input checked={costConfirmed} onChange={(event) => setCostConfirmed(event.target.checked)} type="checkbox" /><span>我确认可能产生少量上游费用</span></label> : <label className="field"><span className="field-label">原因</span><textarea className="input textarea-input" onChange={(event) => setReason(event.target.value)} required value={reason} /></label>}
    </ConfirmDialog>
  </AppShell>
}
