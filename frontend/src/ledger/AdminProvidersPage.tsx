import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api/client'
import type { ProviderIncomeSnapshot } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { InlineError, LoadingState } from '../ui/FormControls'
import { formatPointAmount } from '../wallet/presentation'
import { formatRate } from '../gateway/presentation'

function lastThirtyDays() {
  const to = new Date()
  const from = new Date(to.getTime() - 30 * 24 * 3600 * 1000)
  return { from: from.toISOString(), to: to.toISOString() }
}

export function AdminProvidersPage() {
  const [snapshot, setSnapshot] = useState<ProviderIncomeSnapshot | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    const bounds = lastThirtyDays()
    setLoading(true)
    setError('')
    try {
      setSnapshot(await api.opsProviderIncome(bounds.from, bounds.to))
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '共享者收入加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <AppShell admin>
      <header className="page-heading">
        <div><h1>共享者收入</h1></div>
        <span className="count-badge">近 30 天</span>
      </header>
      <InlineError>{error}</InlineError>
      {loading ? <LoadingState /> : snapshot ? (
        <>
          <section className="metric-grid">
            <article className="metric-card metric-card-accent">
              <span>共享者总收入</span>
              <strong>{formatPointAmount(snapshot.total_income)}</strong>
            </article>
            <article className="metric-card">
              <span>外部消费收入</span>
              <strong>{formatPointAmount(snapshot.other_consumer_income)}</strong>
            </article>
            <article className="metric-card">
              <span>自有调用收入</span>
              <strong>{formatPointAmount(snapshot.own_usage_income)}</strong>
            </article>
            <article className="metric-card">
              <span>活跃共享者</span>
              <strong>{snapshot.active_providers}</strong>
            </article>
          </section>
          <section className="panel table-panel">
            <header className="table-toolbar">
              <h2>按共享者</h2>
              <span className="count-badge">{snapshot.providers.length}</span>
            </header>
            {snapshot.providers.length === 0 ? (
              <div className="empty-state">窗口内没有共享者收入</div>
            ) : (
              <>
                <div className="desktop-table-wrap">
                  <table className="data-table">
                    <thead>
                      <tr>
                        <th scope="col">共享者</th>
                        <th scope="col">总收入</th>
                        <th scope="col">外部消费</th>
                        <th scope="col">自有调用</th>
                        <th scope="col">成功率</th>
                      </tr>
                    </thead>
                    <tbody>
                      {snapshot.providers.map((row) => (
                        <tr key={row.account_id}>
                          <td><strong>{row.display_name}</strong></td>
                          <td>{formatPointAmount(row.total_income)}</td>
                          <td>{formatPointAmount(row.other_consumer_income)}</td>
                          <td>{formatPointAmount(row.own_usage_income)}</td>
                          <td>{row.success_rate === null ? '—' : formatRate(row.success_rate)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <div className="mobile-card-list">
                  {snapshot.providers.map((row) => (
                    <article className="mobile-data-card" key={row.account_id}>
                      <header>
                        <div>
                          <strong>{row.display_name}</strong>
                          <span>总收入 {formatPointAmount(row.total_income)}</span>
                        </div>
                      </header>
                      <dl>
                        <div><dt>外部消费</dt><dd>{formatPointAmount(row.other_consumer_income)}</dd></div>
                        <div><dt>自有调用</dt><dd>{formatPointAmount(row.own_usage_income)}</dd></div>
                        <div><dt>成功率</dt><dd>{row.success_rate === null ? '—' : formatRate(row.success_rate)}</dd></div>
                      </dl>
                    </article>
                  ))}
                </div>
              </>
            )}
          </section>
        </>
      ) : null}
    </AppShell>
  )
}
