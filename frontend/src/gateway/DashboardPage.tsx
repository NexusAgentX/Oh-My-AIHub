import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { GatewayDashboard } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { InlineError, LoadingState } from '../ui/FormControls'
import { useWallet } from '../wallet/WalletProvider'
import { formatPointAmount } from '../wallet/presentation'
import { formatNanoPoints, parseNanoPoints } from '../money/amount'
import { CallTable } from './CallTable'
import { formatPoints } from './presentation'

function remainingCredit(limit: string, used: string) {
  const leftover = parseNanoPoints(limit) - parseNanoPoints(used)
  return formatNanoPoints(leftover > 0n ? leftover : 0n)
}

export function DashboardPage() {
  const { wallet } = useWallet()
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
      <header className="page-heading workspace-heading">
        <div>
          <h1>工作台</h1>
          <p>消费、共享和交易的今日状态</p>
        </div>
        <div className="workspace-heading-actions">
          <Link className="button button-secondary" to="/market">浏览 API 市场</Link>
          <Link className="button button-primary" to="/keys/new">创建 API Key</Link>
        </div>
      </header>
      <InlineError>{error}</InlineError>
      {loading ? <LoadingState /> : dashboard && (
        <>
          <section className="metric-grid">
            <article className="metric-card metric-card-accent">
              <span>可用余额</span>
              <strong>{formatPointAmount(wallet?.spendable_capacity ?? '0')}</strong>
              <small>冻结 {formatPointAmount(wallet?.asset_reserved ?? '0')}</small>
            </article>
            <article className="metric-card metric-card-warm">
              <span>剩余信用</span>
              <strong>{formatPointAmount(wallet ? remainingCredit(wallet.effective_credit_limit, wallet.credit_used) : '0')}</strong>
              <small>额度 {formatPointAmount(wallet?.credit_limit ?? '0')}</small>
            </article>
            <article className="metric-card">
              <span>今日消费</span>
              <strong>{formatPoints(dashboard.today_spent)}</strong>
              <small>{dashboard.today_succeeded_calls} 次成功调用</small>
            </article>
            <article className="metric-card">
              <span>今日渠道收入</span>
              <strong>{formatPoints(dashboard.today_external_provider_income)}</strong>
              <small>不含自有调用</small>
            </article>
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
              <header className="panel-heading"><h2>API Key 与模型协议池</h2></header>
              <div className="health-stat-grid health-stat-grid-compact">
                <div><span>活跃 API Keys</span><strong>{dashboard.active_key_count}</strong></div>
                <div><span>模型协议池</span><strong>{dashboard.pool_count}</strong></div>
              </div>
            </article>
          </section>
          <section className="panel table-panel dashboard-recent-panel">
            <header className="table-toolbar"><h2>最近调用</h2><Link to="/calls">全部记录</Link></header>
            <CallTable calls={dashboard.recent_calls} />
          </section>
        </>
      )}
    </AppShell>
  )
}
