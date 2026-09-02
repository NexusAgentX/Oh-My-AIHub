import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { Channel } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { InlineError, LoadingState } from '../ui/FormControls'
import { Icon } from '../ui/Icon'
import { ChannelStateBadge, formatDate, ratingText } from './presentation'

export function ChannelsPage() {
  const [channels, setChannels] = useState<Channel[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    api.channels()
      .then((items) => active && setChannels(items))
      .catch((caught) => active && setError(caught instanceof ApiError ? caught.message : '渠道加载失败'))
      .finally(() => active && setLoading(false))
    return () => { active = false }
  }, [])

  const metrics = useMemo(() => {
    const live = channels.filter((item) => item.status !== 'deleted')
    return {
      total: live.length,
      published: live.filter((item) => item.status === 'published').length,
      offers: live.reduce((sum, item) => sum + item.offers.filter((offer) => offer.status === 'active').length, 0),
      available: live.reduce((sum, item) => sum + item.offers.filter((offer) => offer.eligible).length, 0),
    }
  }, [channels])

  return (
    <AppShell>
      <header className="page-heading">
        <div><h1>我的渠道</h1></div>
        <Link className="button button-primary" to="/channels/new">
          <Icon name="plus" /><span>上架渠道</span>
        </Link>
      </header>
      <InlineError>{error}</InlineError>
      <section aria-label="渠道概况" className="metric-grid">
        <article className="metric-card"><span>渠道</span><strong>{metrics.total}</strong><small>可管理</small></article>
        <article className="metric-card metric-card-accent"><span>已发布</span><strong>{metrics.published}</strong><small>生命周期状态</small></article>
        <article className="metric-card"><span>协议报价</span><strong>{metrics.offers}</strong><small>当前启用</small></article>
        <article className="metric-card"><span>当前可用</span><strong>{metrics.available}</strong><small>可加入模型池</small></article>
      </section>
      <section className="panel table-panel">
        <header className="table-toolbar"><h2>渠道列表</h2></header>
        {loading ? <LoadingState /> : channels.length === 0 ? (
          <div className="empty-state">还没有渠道</div>
        ) : (
          <>
            <div className="desktop-table-wrap">
              <table className="data-table">
                <thead><tr><th>渠道</th><th>状态</th><th>报价</th><th>评分</th><th>更新</th><th><span className="visually-hidden">操作</span></th></tr></thead>
                <tbody>
                  {channels.map((item) => (
                    <tr key={item.id}>
                      <td><strong>{item.display_name}</strong><small>{item.credential_configured ? '凭据已配置' : '凭据未配置'}</small></td>
                      <td><ChannelStateBadge status={item.status} /></td>
                      <td>{item.offers.filter((offer) => offer.status !== 'deleted').length}</td>
                      <td>{ratingText(item.average_rating, item.rating_count)}</td>
                      <td>{formatDate(item.updated_at)}</td>
                      <td className="table-action"><Link className="button button-secondary" to={`/channels/${item.id}`}>查看</Link></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="mobile-card-list">
              {channels.map((item) => (
                <article className="mobile-data-card" key={item.id}>
                  <header><div><strong>{item.display_name}</strong><span>{formatDate(item.updated_at)}</span></div><ChannelStateBadge status={item.status} /></header>
                  <dl>
                    <div><dt>协议报价</dt><dd>{item.offers.filter((offer) => offer.status !== 'deleted').length}</dd></div>
                    <div><dt>评分</dt><dd>{ratingText(item.average_rating, item.rating_count)}</dd></div>
                  </dl>
                  <Link className="button button-secondary" to={`/channels/${item.id}`}>查看渠道</Link>
                </article>
              ))}
            </div>
          </>
        )}
      </section>
    </AppShell>
  )
}
