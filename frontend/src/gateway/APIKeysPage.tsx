import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { APIKey } from '../api/contracts'
import { formatDate } from '../channels/presentation'
import { AppShell } from '../layouts/AppShell'
import { InlineError, LoadingState } from '../ui/FormControls'
import { Icon } from '../ui/Icon'
import { GatewayStatusBadge } from './presentation'

export function APIKeysPage() {
  const [keys, setKeys] = useState<APIKey[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setKeys(await api.apiKeys())
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : 'API Key 加载失败')
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
        <div>
          <h1>API Key</h1>
        </div>
        <Link className="button button-primary" to="/keys/new">
          <Icon name="plus" /> 新建 Key
        </Link>
      </header>
      <InlineError>{error}</InlineError>
      <section className="panel table-panel">
        <header className="table-toolbar">
          <h2>访问凭据</h2>
          <span className="count-badge">{keys.length}</span>
        </header>
        {loading ? (
          <LoadingState />
        ) : keys.length === 0 ? (
          <div className="empty-state">
            <strong>还没有 API Key</strong>
            <Link className="button button-primary" to="/keys/new">
              创建第一个 Key
            </Link>
          </div>
        ) : (
          <>
            <div className="desktop-table-wrap">
              <table className="data-table">
                <thead>
                  <tr>
                    <th scope="col">名称 / 前缀</th>
                    <th scope="col">状态</th>
                    <th scope="col">模型协议池</th>
                    <th scope="col">最后调用</th>
                    <th scope="col">
                      <span className="visually-hidden">操作</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {keys.map((key) => (
                    <tr key={key.id}>
                      <td>
                        <strong>{key.display_name}</strong>
                        <small className="mono-value">{key.prefix}…</small>
                      </td>
                      <td>
                        <GatewayStatusBadge status={key.status} />
                      </td>
                      <td>{key.pools.length}</td>
                      <td>{formatDate(key.last_used_at)}</td>
                      <td className="table-action">
                        <Link
                          className="button button-secondary"
                          to={`/keys/${key.id}`}
                        >
                          查看
                        </Link>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="mobile-card-list">
              {keys.map((key) => (
                <article className="mobile-data-card" key={key.id}>
                  <header>
                    <div>
                      <strong>{key.display_name}</strong>
                      <span className="mono-value">{key.prefix}…</span>
                    </div>
                    <GatewayStatusBadge status={key.status} />
                  </header>
                  <dl>
                    <div>
                      <dt>模型协议池</dt>
                      <dd>{key.pools.length}</dd>
                    </div>
                    <div>
                      <dt>最后调用</dt>
                      <dd>{formatDate(key.last_used_at)}</dd>
                    </div>
                  </dl>
                  <Link className="button button-secondary" to={`/keys/${key.id}`}>
                    查看 Key
                  </Link>
                </article>
              ))}
            </div>
          </>
        )}
      </section>
    </AppShell>
  )
}
