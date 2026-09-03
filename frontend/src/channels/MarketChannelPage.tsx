import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { MarketChannel, MarketOffer } from '../api/contracts'
import { useAuth } from '../auth/AuthProvider'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState } from '../ui/FormControls'
import { ChannelStateBadge, formatDate, PricePair, protocolLabels, ratingText, StarRating } from './presentation'
import { createLatestRequestGate } from './requestGate'
import { AddOfferToKeyDialog } from '../gateway/AddOfferToKeyDialog'
import { formatRate } from '../gateway/presentation'

export function MarketChannelPage() {
  const { account } = useAuth()
  const { channelID = '' } = useParams()
  const [channel, setChannel] = useState<MarketChannel | null>(null)
  const [loading, setLoading] = useState(true)
  const [ratingBusy, setRatingBusy] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [offerToAdd, setOfferToAdd] = useState<MarketOffer | null>(null)
  const loadGate = useRef(createLatestRequestGate())
  const ratingGate = useRef(createLatestRequestGate())

  const load = useCallback(async (clear = false) => {
    const ticket = loadGate.current.begin()
    if (clear) {
      setLoading(true)
      setChannel(null)
    }
    setError('')
    try {
      const loaded = await api.marketChannel(channelID)
      if (loadGate.current.isCurrent(ticket)) setChannel(loaded)
    } catch (caught) {
      if (loadGate.current.isCurrent(ticket)) {
        setError(caught instanceof ApiError ? caught.message : '渠道详情加载失败')
      }
    } finally {
      if (loadGate.current.isCurrent(ticket)) setLoading(false)
    }
  }, [channelID])

  useEffect(() => {
    ratingGate.current.invalidate()
    setRatingBusy(false)
    setMessage('')
    void load(true)
    return () => {
      loadGate.current.invalidate()
      ratingGate.current.invalidate()
    }
  }, [load])

  const rate = async (score: number) => {
    const ticket = ratingGate.current.begin()
    setRatingBusy(true)
    setError('')
    setMessage('')
    try {
      const rated = await api.rateChannel(channelID, score)
      if (!ratingGate.current.isCurrent(ticket)) return
      setChannel(rated)
      setMessage('评分已保存')
    } catch (caught) {
      if (ratingGate.current.isCurrent(ticket)) {
        setError(caught instanceof ApiError ? caught.message : '评分保存失败')
      }
    } finally {
      if (ratingGate.current.isCurrent(ticket)) setRatingBusy(false)
    }
  }

  if (loading) return <AppShell><LoadingState /></AppShell>
  if (!channel || channel.id !== channelID) return <AppShell><InlineError>{error || '渠道不存在'}</InlineError></AppShell>

  return (
    <AppShell>
      <Link className="back-link" to="/market">← 渠道市场</Link>
      <header className="page-heading channel-detail-heading"><div><h1>{channel.display_name}</h1><ChannelStateBadge status={channel.status} /></div></header>
      <InlineError>{error}</InlineError>
      {message && <div aria-live="polite" className="success-message">{message}</div>}
      <section className="panel market-channel-summary">
        <div><span className="avatar" aria-hidden="true">{channel.owner_display_name.slice(0, 1)}</span><div><strong>{channel.owner_display_name}</strong><span>共享者</span>{channel.owner_account_id === account?.id && <span className="own-channel-badge">我的 · 0 手续费</span>}</div></div>
        <div><strong>{ratingText(channel.average_rating, channel.rating_count)}</strong><StarRating disabled={ratingBusy} onChange={(score) => void rate(score)} value={channel.current_user_rating} /></div>
      </section>
      <section className="panel table-panel market-channel-offers">
        <header className="table-toolbar"><h2>可用报价</h2><span className="count-badge">{channel.offers.length}</span></header>
        {channel.offers.length === 0 ? <div className="empty-state">渠道当前不可用</div> : <>
          <div className="desktop-table-wrap"><table className="data-table"><thead><tr><th scope="col">模型</th><th scope="col">API 格式</th><th scope="col">倍率</th><th scope="col">输入 / 输出</th><th scope="col">缓存写 / 读</th><th scope="col">质量</th><th scope="col"><span className="visually-hidden">操作</span></th></tr></thead><tbody>{channel.offers.map((offer) => <tr key={offer.offer_id}>
            <td><strong>{offer.model_name}</strong><small>{offer.model_provider}</small></td><td>{protocolLabels[offer.protocol]}</td><td>{offer.multiplier}×</td><td><PricePair first={offer.input_price} second={offer.output_price} /></td><td><PricePair first={offer.cache_write_price} second={offer.cache_read_price} /></td><td>{offer.call_success_rate === null ? <><span className="quality-empty">暂无调用数据</span><small>验证 {formatDate(offer.last_tested_at)}</small></> : `${formatRate(offer.call_success_rate)} · ${offer.call_count ?? 0} 次 · ${offer.ttft_milliseconds ?? '—'} ms`}</td><td className="table-action"><Button onClick={() => setOfferToAdd(offer)} type="button" variant="secondary">加入模型池</Button></td>
          </tr>)}</tbody></table></div>
          <div className="mobile-card-list">{channel.offers.map((offer) => <article className="mobile-data-card" key={offer.offer_id}><header><div><strong>{offer.model_name}</strong><span>{offer.model_provider}</span></div><ChannelStateBadge status={offer.validation_status} /></header><dl><div><dt>API 格式</dt><dd>{protocolLabels[offer.protocol]}</dd></div><div><dt>倍率</dt><dd>{offer.multiplier}×</dd></div><div><dt>输入 / 输出</dt><dd><PricePair first={offer.input_price} second={offer.output_price} /></dd></div><div><dt>缓存写 / 读</dt><dd><PricePair first={offer.cache_write_price} second={offer.cache_read_price} /></dd></div><div><dt>质量</dt><dd>{offer.call_success_rate === null ? '暂无调用数据' : formatRate(offer.call_success_rate)}</dd></div></dl><Button onClick={() => setOfferToAdd(offer)} type="button" variant="secondary">加入模型池</Button></article>)}</div>
        </>}
      </section>
      <AddOfferToKeyDialog offer={offerToAdd} onAdded={(key) => { setMessage(`已加入 ${key.display_name}`); setOfferToAdd(null) }} onCancel={() => setOfferToAdd(null)} />
    </AppShell>
  )
}
