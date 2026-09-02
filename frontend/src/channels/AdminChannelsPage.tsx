import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { AdminChannel } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { InlineError, LoadingState } from '../ui/FormControls'
import { ChannelStateBadge, formatDate, ratingText } from './presentation'

export function AdminChannelsPage() {
  const [channels, setChannels] = useState<AdminChannel[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    api.adminChannels().then((items) => active && setChannels(items)).catch((caught) => {
      if (active) setError(caught instanceof ApiError ? caught.message : '渠道治理列表加载失败')
    }).finally(() => active && setLoading(false))
    return () => { active = false }
  }, [])

  const metrics = useMemo(() => ({
    total: channels.length,
    published: channels.filter((item) => item.status === 'published').length,
    paused: channels.filter((item) => item.status === 'paused').length,
    missingCredential: channels.filter((item) => item.status !== 'deleted' && !item.credential_configured).length,
  }), [channels])

  return <AppShell admin>
    <header className="page-heading"><div><h1>渠道治理</h1></div></header>
    <InlineError>{error}</InlineError>
    <section aria-label="渠道治理概况" className="metric-grid"><article className="metric-card"><span>全部渠道</span><strong>{metrics.total}</strong><small>含历史状态</small></article><article className="metric-card metric-card-accent"><span>已发布</span><strong>{metrics.published}</strong><small>生命周期状态</small></article><article className="metric-card metric-card-warm"><span>已暂停</span><strong>{metrics.paused}</strong><small>待处理</small></article><article className="metric-card"><span>凭据缺失</span><strong>{metrics.missingCredential}</strong><small>不可路由</small></article></section>
    <section className="panel table-panel"><header className="table-toolbar"><h2>渠道</h2></header>{loading ? <LoadingState /> : channels.length === 0 ? <div className="empty-state">没有渠道</div> : <><div className="desktop-table-wrap"><table className="data-table"><thead><tr><th scope="col">渠道</th><th scope="col">共享者</th><th scope="col">状态</th><th scope="col">报价</th><th scope="col">评分</th><th scope="col">更新</th><th scope="col"><span className="visually-hidden">操作</span></th></tr></thead><tbody>{channels.map((item) => <tr key={item.id}><td><strong>{item.display_name}</strong><small>{item.credential_configured ? `凭据 v${item.credential_version}` : '凭据缺失'}</small></td><td>{item.owner_display_name}</td><td><ChannelStateBadge status={item.status} /></td><td>{item.offers.filter((offer) => offer.status !== 'deleted').length}</td><td>{ratingText(item.average_rating, item.rating_count)}</td><td>{formatDate(item.updated_at)}</td><td className="table-action"><Link className="button button-secondary" to={`/admin/channels/${item.id}`}>治理</Link></td></tr>)}</tbody></table></div><div className="mobile-card-list">{channels.map((item) => <article className="mobile-data-card" key={item.id}><header><div><strong>{item.display_name}</strong><span>{item.owner_display_name}</span></div><ChannelStateBadge status={item.status} /></header><dl><div><dt>报价</dt><dd>{item.offers.filter((offer) => offer.status !== 'deleted').length}</dd></div><div><dt>凭据</dt><dd>{item.credential_configured ? '已配置' : '缺失'}</dd></div><div><dt>评分</dt><dd>{ratingText(item.average_rating, item.rating_count)}</dd></div></dl><Link className="button button-secondary" to={`/admin/channels/${item.id}`}>查看治理</Link></article>)}</div></>}</section>
  </AppShell>
}
