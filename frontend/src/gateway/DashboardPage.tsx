import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { GatewayDashboard } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { InlineError, LoadingState } from '../ui/FormControls'
import { Icon } from '../ui/Icon'
import { CallTable } from './CallTable'
import { formatPoints } from './presentation'

export function DashboardPage() {
  const [dashboard, setDashboard] = useState<GatewayDashboard | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setDashboard(await api.gatewayDashboard())
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '总览加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <AppShell>
      <header className="page-heading">
        <div><h1>总览</h1></div>
        <Link className="button button-primary" to="/keys/new"><Icon name="plus" /> 新建 API Key</Link>
      </header>
      <InlineError>{error}</InlineError>
      {loading ? <LoadingState /> : dashboard && (
        <>
          <section className="metric-grid">
            <article className="metric-card metric-card-accent"><span>累计 API 消费</span><strong>{formatPoints(dashboard.consumer_spent)}</strong><small>积分</small></article>
            <article className="metric-card"><span>累计渠道收入</span><strong>{formatPoints(dashboard.provider_income)}</strong><small>积分</small></article>
            <article className="metric-card"><span>活跃 API Key</span><strong>{dashboard.active_key_count}</strong><small><Link to="/keys">管理 Key</Link></small></article>
            <article className="metric-card"><span>模型协议池</span><strong>{dashboard.pool_count}</strong><small>固定优先级路由</small></article>
          </section>
          <section className="dashboard-grid">
            <article className="panel dashboard-health-panel">
              <header className="panel-heading"><h2>渠道健康</h2><Link to="/market">打开市场</Link></header>
              <div className="health-stat-grid">
                <div><span>可用</span><strong>{dashboard.healthy_offer_count}</strong></div>
                <div><span>需处理</span><strong>{dashboard.unhealthy_offer_count}</strong></div>
                <div><span>待处理事项</span><strong>{dashboard.pending_items}</strong></div>
              </div>
            </article>
            <article className="panel dashboard-actions-panel">
              <header className="panel-heading"><h2>快捷入口</h2></header>
              <nav aria-label="快捷入口" className="dashboard-actions">
                <Link to="/keys">API Key</Link>
                <Link to="/market">渠道市场</Link>
                <Link to="/channels">我的渠道</Link>
                <Link to="/wallet">积分钱包</Link>
              </nav>
            </article>
          </section>
          <section className="panel table-panel dashboard-recent-panel">
            <header className="table-toolbar"><h2>最近调用</h2><Link to="/calls">全部调用</Link></header>
            <CallTable calls={dashboard.recent_calls} />
          </section>
        </>
      )}
    </AppShell>
  )
}
