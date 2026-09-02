import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { CatalogModel, ChannelProtocol, MarketOffer } from '../api/contracts'
import { useAuth } from '../auth/AuthProvider'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState } from '../ui/FormControls'
import { ChannelStateBadge, formatDate, PricePair, protocolLabels, ratingText } from './presentation'

type Filters = {
  modelID: string
  protocol: ChannelProtocol | ''
  owner: string
  sort: 'input_price' | 'output_price' | 'cache_write_price' | 'cache_read_price' | 'rating'
}

const initialFilters: Filters = { modelID: '', protocol: '', owner: '', sort: 'input_price' }

export function MarketPage() {
  const { account } = useAuth()
  const [models, setModels] = useState<CatalogModel[]>([])
  const [draft, setDraft] = useState<Filters>(initialFilters)
  const [filters, setFilters] = useState<Filters>(initialFilters)
  const [offers, setOffers] = useState<MarketOffer[]>([])
  const [next, setNext] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const requestGeneration = useRef(0)

  const load = async (activeFilters: Filters, after = '') => {
    const generation = after ? requestGeneration.current : ++requestGeneration.current
    setLoading(true)
    setError('')
    try {
      const result = await api.marketOffers({ ...activeFilters, after, limit: 20 })
      if (generation !== requestGeneration.current) return
      setOffers((current) => {
        const combined = after ? [...current, ...result.offers] : result.offers
        return [...new Map(combined.map((offer) => [offer.offer_id, offer])).values()]
      })
      setNext(result.next_after)
    } catch (caught) {
      if (generation !== requestGeneration.current) return
      setError(caught instanceof ApiError ? caught.message : '渠道市场加载失败')
    } finally {
      if (generation === requestGeneration.current) setLoading(false)
    }
  }

  useEffect(() => {
    void api.models().then(setModels).catch(() => undefined)
    void load(initialFilters)
    return () => { requestGeneration.current += 1 }
  }, [])

  const applyFilters = (event: FormEvent) => {
    event.preventDefault()
    setFilters(draft)
    setOffers([])
    setNext('')
    void load(draft)
  }

  return (
    <AppShell>
      <header className="page-heading"><div><h1>渠道市场</h1></div><Link className="button button-secondary" to="/channels">我的渠道</Link></header>
      <InlineError>{error}</InlineError>
      <section className="panel market-filter-panel">
        <form className="market-filter-grid" onSubmit={applyFilters}>
          <label className="field"><span className="field-label">模型</span><select className="input" onChange={(event) => setDraft({ ...draft, modelID: event.target.value })} value={draft.modelID}><option value="">全部模型</option>{models.map((model) => <option key={model.id} value={model.id}>{model.provider} · {model.name}</option>)}</select></label>
          <label className="field"><span className="field-label">API 格式</span><select className="input" onChange={(event) => setDraft({ ...draft, protocol: event.target.value as ChannelProtocol | '' })} value={draft.protocol}><option value="">全部格式</option>{Object.entries(protocolLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
          <label className="field"><span className="field-label">共享者</span><input className="input" onChange={(event) => setDraft({ ...draft, owner: event.target.value })} value={draft.owner} /></label>
          <label className="field"><span className="field-label">排序</span><select className="input" onChange={(event) => setDraft({ ...draft, sort: event.target.value as Filters['sort'] })} value={draft.sort}><option value="input_price">输入价格</option><option value="output_price">输出价格</option><option value="cache_write_price">缓存写价格</option><option value="cache_read_price">缓存读价格</option><option value="rating">用户评分</option></select></label>
          <Button type="submit">筛选</Button>
        </form>
      </section>

      <section className="panel table-panel market-offers-panel">
        <header className="table-toolbar"><h2>可用报价</h2><span className="count-badge">{offers.length}</span></header>
        {loading && offers.length === 0 ? <LoadingState /> : offers.length === 0 ? <div className="empty-state">没有匹配的报价</div> : <>
          <div className="desktop-table-wrap">
            <table className="data-table market-table">
              <thead><tr><th scope="col">模型 / 渠道</th><th scope="col">API 格式</th><th scope="col">倍率</th><th scope="col">输入 / 输出</th><th scope="col">缓存写 / 读</th><th scope="col">质量</th><th scope="col">评分</th><th scope="col"><span className="visually-hidden">操作</span></th></tr></thead>
              <tbody>{offers.map((offer) => <tr key={offer.offer_id}>
                <td><strong>{offer.model_name}</strong><small>{offer.channel_display_name} · {offer.owner_display_name}</small>{offer.owner_account_id === account?.id && <span className="own-channel-badge">我的 · 0 手续费</span>}</td>
                <td>{protocolLabels[offer.protocol]}</td>
                <td>{offer.multiplier}×</td>
                <td><PricePair first={offer.input_price} second={offer.output_price} /></td>
                <td><PricePair first={offer.cache_write_price} second={offer.cache_read_price} /></td>
                <td>{offer.call_success_rate === null ? <><span className="quality-empty">暂无调用数据</span><small>验证 {formatDate(offer.last_tested_at)}</small></> : <><strong>{offer.call_success_rate}</strong><small>{offer.ttft_milliseconds ?? '—'} ms · {offer.tokens_per_second ?? '—'} tok/s</small></>}</td>
                <td>{ratingText(offer.average_rating, offer.rating_count)}</td>
                <td className="table-action"><Link className="button button-secondary" to={`/market/channels/${offer.channel_id}`}>查看</Link></td>
              </tr>)}</tbody>
            </table>
          </div>
          <div className="mobile-card-list">{offers.map((offer) => <article className="mobile-data-card" key={offer.offer_id}>
            <header><div><strong>{offer.model_name}</strong><span>{offer.channel_display_name} · {offer.owner_display_name}</span>{offer.owner_account_id === account?.id && <span className="own-channel-badge">我的 · 0 手续费</span>}</div><ChannelStateBadge status={offer.validation_status} /></header>
            <dl><div><dt>API 格式</dt><dd>{protocolLabels[offer.protocol]}</dd></div><div><dt>倍率</dt><dd>{offer.multiplier}×</dd></div><div><dt>输入 / 输出</dt><dd><PricePair first={offer.input_price} second={offer.output_price} /></dd></div><div><dt>质量</dt><dd>{offer.call_success_rate === null ? '暂无调用数据' : offer.call_success_rate}</dd></div><div><dt>评分</dt><dd>{ratingText(offer.average_rating, offer.rating_count)}</dd></div></dl>
            <Link className="button button-secondary" to={`/market/channels/${offer.channel_id}`}>渠道详情</Link>
          </article>)}</div>
          {next && <div className="table-pagination"><Button disabled={loading} onClick={() => void load(filters, next)} variant="secondary">{loading ? '正在加载' : '加载更多'}</Button></div>}
        </>}
      </section>
    </AppShell>
  )
}
