import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { APIKey, MarketChannel } from '../api/contracts'
import { formatDate, PricePair, protocolLabels, ratingText } from '../channels/presentation'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState } from '../ui/FormControls'
import { formatRate } from './presentation'

export function AddOfferToPoolPage() {
  const { channelID = '' } = useParams()
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const requestedOfferID = searchParams.get('offer') ?? ''
  const [channel, setChannel] = useState<MarketChannel | null>(null)
  const [keys, setKeys] = useState<APIKey[]>([])
  const [selectedKeyID, setSelectedKeyID] = useState('')
  const [priority, setPriority] = useState(1)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    setLoading(true)
    setError('')
    Promise.all([api.marketChannel(channelID), api.apiKeys()])
      .then(([loadedChannel, loadedKeys]) => {
        if (!active) return
        setChannel(loadedChannel)
        const available = loadedKeys.filter((key) => key.status !== 'deleted')
        setKeys(available)
        setSelectedKeyID(available[0]?.id ?? '')
      })
      .catch((caught) => {
        if (active) {
          setError(caught instanceof ApiError ? caught.message : '加入模型池加载失败')
        }
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [channelID])

  const offer = useMemo(() => {
    if (!channel) return null
    return (
      channel.offers.find((item) => item.offer_id === requestedOfferID) ??
      channel.offers[0] ??
      null
    )
  }, [channel, requestedOfferID])

  const selectedKey = keys.find((key) => key.id === selectedKeyID)
  const matchingPool = selectedKey?.pools.find(
    (pool) =>
      offer &&
      pool.model_id === offer.model_id &&
      pool.protocol === offer.protocol,
  )
  const alreadyIncluded = Boolean(
    matchingPool?.members.some((member) => member.offer_id === offer?.offer_id),
  )
  const unavailable = Boolean(
    channel && (channel.status !== 'published' || channel.offers.length === 0 || !offer),
  )
  const maxPriority = (matchingPool?.members.length ?? 0) + 1

  useEffect(() => {
    setPriority(maxPriority)
  }, [maxPriority, selectedKeyID, offer?.offer_id])

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!offer || !selectedKey || unavailable || alreadyIncluded) return
    setBusy(true)
    setError('')
    try {
      await api.addAPIKeyPoolMember(selectedKey.id, selectedKey.version, {
        model_id: offer.model_id,
        protocol: offer.protocol,
        offer_id: offer.offer_id,
        priority,
      })
      navigate(`/keys/${selectedKey.id}`)
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '加入模型池失败')
    } finally {
      setBusy(false)
    }
  }

  if (loading) return <AppShell><LoadingState /></AppShell>
  if (!channel) return <AppShell><InlineError>{error || '渠道不存在'}</InlineError></AppShell>

  return (
    <AppShell>
      <Link className="back-link" to={`/market/channels/${channel.id}`}>← {channel.display_name}</Link>
      <header className="page-heading channel-detail-heading">
        <div>
          <h1>加入模型协议池</h1>
        </div>
        <span className="count-badge">{unavailable ? '不可加入' : '兼容'}</span>
      </header>
      <InlineError>{error}</InlineError>
      <div className="add-pool-layout">
        <section className="panel add-pool-offer">
          <header className="panel-heading">
            <div>
              <h2>{channel.display_name}</h2>
              <small>{channel.owner_display_name} · {ratingText(channel.average_rating, channel.rating_count)}</small>
            </div>
            <span>{unavailable ? '不可加入' : '兼容'}</span>
          </header>
          {offer ? (
            <dl className="detail-list">
              <div><dt>模型 · 原生格式</dt><dd>{offer.model_name} · {protocolLabels[offer.protocol]}</dd></div>
              <div><dt>输入 / 输出</dt><dd><PricePair first={offer.input_price} second={offer.output_price} /></dd></div>
              <div><dt>平台倍率</dt><dd>{offer.multiplier}×</dd></div>
              <div><dt>成功率</dt><dd>{offer.call_success_rate === null ? '—' : formatRate(offer.call_success_rate)}</dd></div>
              <div><dt>P50 TTFT</dt><dd>{offer.ttft_milliseconds === null ? '—' : `${offer.ttft_milliseconds} ms`}</dd></div>
              <div><dt>输出速度</dt><dd>{offer.tokens_per_second === null ? '—' : `${offer.tokens_per_second} TPS`}</dd></div>
              <div><dt>失败处理</dt><dd>未成功不计费</dd></div>
              <div><dt>最近检测</dt><dd>{formatDate(offer.last_tested_at)}</dd></div>
            </dl>
          ) : (
            <div className="empty-state">渠道当前没有可加入的报价</div>
          )}
        </section>
        <form className="panel add-pool-form" onSubmit={(event) => void submit(event)}>
          <header className="panel-heading"><h2>目标模型协议池</h2></header>
          <div className="stack-form">
            <label className="field">
              <span className="field-label">API Key</span>
              <select
                className="input"
                disabled={unavailable || keys.length === 0}
                onChange={(event) => setSelectedKeyID(event.target.value)}
                value={selectedKeyID}
              >
                <option value="">{keys.length === 0 ? '没有可用 Key' : '选择 Key'}</option>
                {keys.map((key) => (
                  <option key={key.id} value={key.id}>{key.display_name}</option>
                ))}
              </select>
            </label>
            <label className="field">
              <span className="field-label">优先级</span>
              <select
                className="input"
                disabled={unavailable || alreadyIncluded || !selectedKey}
                onChange={(event) => setPriority(Number(event.target.value))}
                value={priority}
              >
                {Array.from({ length: maxPriority }, (_, index) => index + 1).map((value) => (
                  <option key={value} value={value}>
                    {value === 1 ? '1 · 首选' : `${value} · 回退`}
                  </option>
                ))}
              </select>
            </label>
            {matchingPool && (
              <ol className="add-pool-order">
                {matchingPool.members.map((member, index) => (
                  <li key={member.offer_id}>
                    <span>{String(index + 1).padStart(2, '0')}</span>
                    <strong>{member.channel_name}</strong>
                    <small>{index === 0 ? '首选' : '回退'}</small>
                  </li>
                ))}
                {!alreadyIncluded && <li className="add-pool-order-new"><span>{String(priority).padStart(2, '0')}</span><strong>新增</strong></li>}
              </ol>
            )}
            {alreadyIncluded && <p className="muted">这个报价已经在所选 Key 的模型协议池中。</p>}
            {unavailable && <p className="muted">渠道当前不可加入模型协议池。</p>}
            <div className="form-actions">
              <Button
                disabled={busy || unavailable || alreadyIncluded || !selectedKey || !offer}
                type="submit"
              >
                {busy ? '正在加入' : '加入'}
              </Button>
              <Link className="button button-secondary" to={`/market/channels/${channel.id}`}>取消</Link>
            </div>
          </div>
        </form>
      </div>
    </AppShell>
  )
}
