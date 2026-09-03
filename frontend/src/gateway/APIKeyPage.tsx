import { useCallback, useEffect, useState } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { APIKey } from '../api/contracts'
import { formatDate, PricePair, protocolLabels, TierCountBadge, TierPriceList } from '../channels/presentation'
import { AppShell } from '../layouts/AppShell'
import {
  Button,
  InlineError,
  LoadingState,
} from '../ui/FormControls'
import { Icon } from '../ui/Icon'
import { ConfirmDialog } from '../channels/presentation'
import { formatRate, GatewayStatusBadge } from './presentation'

type SecretLocationState = { secret?: string } | null
type PendingAction = 'rotate' | 'disable' | 'enable' | 'delete' | ''

export function APIKeyPage() {
  const { keyID = '' } = useParams()
  const location = useLocation()
  const navigate = useNavigate()
  const [key, setKey] = useState<APIKey | null>(null)
  const [secret, setSecret] = useState(
    (location.state as SecretLocationState)?.secret ?? '',
  )
  const [copied, setCopied] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [pending, setPending] = useState<PendingAction>('')
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setKey(await api.apiKey(keyID))
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : 'API Key 加载失败')
    } finally {
      setLoading(false)
    }
  }, [keyID])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    if ((location.state as SecretLocationState)?.secret) {
      navigate(location.pathname, { replace: true, state: null })
    }
  }, [location.pathname, location.state, navigate])

  const dismissSecret = () => {
    setSecret('')
    setCopied(false)
    navigate(location.pathname, { replace: true, state: null })
  }

  const runAction = async () => {
    if (!key || !pending) return
    setBusy(true)
    setError('')
    try {
      if (pending === 'rotate') {
        const rotated = await api.rotateAPIKey(key.id, key.version)
        setKey(rotated.key)
        setSecret(rotated.secret)
        setCopied(false)
      } else if (pending === 'delete') {
        await api.deleteAPIKey(key.id, key.version)
        setSecret('')
        setPending('')
        navigate('/keys', { replace: true })
        return
      } else {
        setKey(await api.setAPIKeyStatus(key.id, pending, key.version))
      }
      setPending('')
    } catch (caught) {
      setError(
        caught instanceof ApiError && caught.code === 'conflict'
          ? 'Key 状态已变化，请重新加载后重试'
          : caught instanceof ApiError
            ? caught.message
            : '操作失败',
      )
    } finally {
      setBusy(false)
    }
  }

  if (loading) return <AppShell><LoadingState /></AppShell>
  if (!key) {
    return <AppShell><InlineError>{error || 'API Key 不存在'}</InlineError></AppShell>
  }

  return (
    <AppShell>
      <Link className="back-link" to="/keys">← API Key</Link>
      <header className="page-heading channel-detail-heading">
        <div>
          <h1>{key.display_name}</h1>
          <GatewayStatusBadge status={key.status} />
        </div>
        {key.status !== 'deleted' && (
          <Link className="button button-primary" to={`/keys/${key.id}/settings`}>
            <Icon name="settings" /> 编辑配置
          </Link>
        )}
      </header>
      <InlineError>{error}</InlineError>
      {secret && (
        <section className="panel one-time-key-panel">
          <header className="panel-heading">
            <h2>保存新 Key</h2>
            <span className="channel-state channel-state-warning">仅显示一次</span>
          </header>
          <div className="credential-card">
            <div>
              <span>API Key</span>
              <strong>{secret}</strong>
            </div>
          </div>
          <div className="credential-actions">
            <Button
              icon={<Icon name={copied ? 'check' : 'copy'} />}
              onClick={() => {
                void navigator.clipboard.writeText(secret).then(() => setCopied(true))
              }}
              type="button"
              variant="secondary"
            >
              {copied ? '已复制' : '复制 Key'}
            </Button>
            <Button onClick={dismissSecret} type="button" variant="quiet">
              我已保存
            </Button>
          </div>
        </section>
      )}
      <section className="gateway-detail-grid">
        <article className="panel">
          <header className="panel-heading"><h2>Key 概况</h2></header>
          <dl className="detail-list">
            <div><dt>公开前缀</dt><dd className="mono-value">{key.prefix}…</dd></div>
            <div><dt>代次</dt><dd>{key.generation}</dd></div>
            <div><dt>模型协议池</dt><dd>{key.pools.length}</dd></div>
            <div><dt>最后调用</dt><dd>{formatDate(key.last_used_at)}</dd></div>
            <div><dt>更新时间</dt><dd>{formatDate(key.updated_at)}</dd></div>
          </dl>
        </article>
        <article className="panel key-action-panel">
          <header className="panel-heading"><h2>Key 操作</h2></header>
          <div className="key-action-list">
            {key.status !== 'deleted' && (
              <Button onClick={() => setPending('rotate')} type="button" variant="secondary">
                轮换 Key
              </Button>
            )}
            {key.status === 'active' && (
              <Button onClick={() => setPending('disable')} type="button" variant="secondary">
                停用
              </Button>
            )}
            {key.status === 'disabled' && (
              <Button onClick={() => setPending('enable')} type="button" variant="secondary">
                启用
              </Button>
            )}
            {key.status !== 'deleted' && (
              <Button onClick={() => setPending('delete')} type="button" variant="danger">
                删除 Key
              </Button>
            )}
          </div>
        </article>
      </section>
      <section className="panel table-panel key-pool-detail-panel">
        <header className="table-toolbar">
          <h2>模型协议池</h2>
          <span className="count-badge">{key.pools.length}</span>
        </header>
        {key.pools.length === 0 ? (
          <div className="empty-state">没有已配置的模型协议池</div>
        ) : (
          <div className="key-pool-detail-list">
            {key.pools.map((pool) => (
              <article className="key-pool-detail" key={pool.id}>
                <header>
                  <div>
                    <strong>{pool.model_name}</strong>
                    <span>{protocolLabels[pool.protocol]}</span>
                  </div>
                  <span className="count-badge">{pool.members.length}</span>
                </header>
                <div className="desktop-table-wrap">
                  <table className="data-table">
                    <thead>
                      <tr>
                        <th scope="col">优先级 / 渠道</th>
                        <th scope="col">资格</th>
                        <th scope="col">输入 / 输出</th>
                        <th scope="col">缓存写 / 读</th>
                        <th scope="col">成功率</th>
                        <th scope="col">TTFT / TPS</th>
                      </tr>
                    </thead>
                    <tbody>
                      {pool.members.map((member) => (
                        <tr key={member.offer_id}>
                          <td>
                            <strong>#{member.priority} {member.channel_name}</strong>
                            <small>{member.provider_name}</small>
                          </td>
                          <td>
                            <span className={`channel-state channel-state-${member.eligible ? 'positive' : 'warning'}`}>
                              {member.eligible ? '可用' : '需更新'}
                            </span>
                          </td>
                          <td><span className="price-with-tiers"><PricePair first={member.input_price} second={member.output_price} /><TierCountBadge tiers={member.price_tiers} /></span></td>
                          <td><span className="price-with-tiers"><PricePair first={member.cache_write_price} second={member.cache_read_price} /><TierCountBadge tiers={member.price_tiers} /></span></td>
                          <td>{member.success_rate === null ? '—' : formatRate(member.success_rate)}</td>
                          <td>{member.ttft_milliseconds ?? '—'} ms / {member.tokens_per_second ?? '—'}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <div className="mobile-card-list">
                  {pool.members.map((member) => (
                    <article className="mobile-data-card" key={member.offer_id}>
                      <header>
                        <div><strong>#{member.priority} {member.channel_name}</strong><span>{member.provider_name}</span></div>
                        <span className={`channel-state channel-state-${member.eligible ? 'positive' : 'warning'}`}>
                          {member.eligible ? '可用' : '需更新'}
                        </span>
                      </header>
                      <dl>
                        <div><dt>输入 / 输出</dt><dd><PricePair first={member.input_price} second={member.output_price} /><TierCountBadge tiers={member.price_tiers} /></dd></div>
                        <div><dt>成功率</dt><dd>{member.success_rate === null ? '—' : formatRate(member.success_rate)}</dd></div>
                        <div><dt>TTFT / TPS</dt><dd>{member.ttft_milliseconds ?? '—'} ms / {member.tokens_per_second ?? '—'}</dd></div>
                      </dl>
                    </article>
                  ))}
                </div>
              </article>
            ))}
          </div>
        )}
      </section>
      <ConfirmDialog
        busy={busy}
        confirmLabel={
          pending === 'rotate'
            ? '轮换并停用旧 Key'
            : pending === 'delete'
              ? '确认删除'
              : pending === 'disable'
                ? '确认停用'
                : '确认启用'
        }
        danger={pending === 'delete'}
        description={
          pending === 'rotate'
            ? '旧 Key 会立即失效，新 Key 只显示一次。'
            : pending === 'delete'
              ? '删除后不可恢复，历史调用与账单仍会保留。'
              : undefined
        }
        onCancel={() => setPending('')}
        onConfirm={() => void runAction()}
        open={Boolean(pending)}
        title={
          pending === 'rotate'
            ? '轮换 API Key'
            : pending === 'delete'
              ? '删除 API Key'
              : pending === 'disable'
                ? '停用 API Key'
                : '启用 API Key'
        }
      />
    </AppShell>
  )
}
