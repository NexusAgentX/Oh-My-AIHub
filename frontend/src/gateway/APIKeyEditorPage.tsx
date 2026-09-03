import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from 'react'
import { Link, useBlocker, useNavigate, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type {
  APIKey,
  APIKeyPoolInput,
  APIKeyPoolMember,
  CatalogModel,
  ChannelProtocol,
  MarketOffer,
} from '../api/contracts'
import { protocolLabels } from '../channels/presentation'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState, TextField } from '../ui/FormControls'
import { Icon } from '../ui/Icon'
import { formatRate } from './presentation'

const protocols = Object.keys(protocolLabels) as ChannelProtocol[]

type DraftPool = APIKeyPoolInput & { draft_id: string }
export type APIKeyConflictState = {
  localVersion: number
  serverVersion: number | null
}

export function draftID(modelID: string, protocol: ChannelProtocol) {
  return `${modelID}\u0000${protocol}`
}

export function existingMemberMap(key: APIKey) {
  return Object.fromEntries(
    key.pools.flatMap((pool) =>
      pool.members.map((member) => [member.offer_id, member]),
    ),
  ) as Record<string, APIKeyPoolMember>
}

export function draftPoolsFromKey(key: APIKey): DraftPool[] {
  return key.pools.map((pool) => ({
    draft_id: draftID(pool.model_id, pool.protocol),
    model_id: pool.model_id,
    protocol: pool.protocol,
    offer_ids: pool.members.map((member) => member.offer_id),
  }))
}

export function isAPIKeySaveBlocked(
  saving: boolean,
  conflict: APIKeyConflictState | null,
) {
  return saving || conflict !== null
}

export function APIKeyConflictBanner({
  conflict,
  busy,
  onReload,
}: {
  conflict: APIKeyConflictState
  busy: boolean
  onReload: () => void
}) {
  return (
    <section className="conflict-message" role="alert">
      <span>
        服务器版本 v{conflict.serverVersion ?? '未知'}，本地草稿基于 v
        {conflict.localVersion}。草稿仍保留；重新加载前不能保存。
      </span>
      <Button disabled={busy} onClick={onReload} type="button" variant="secondary">
        {busy ? '正在重新加载' : '重新加载最新版本'}
      </Button>
    </section>
  )
}

export function moveOfferIDs(
  offerIDs: string[],
  offerID: string,
  nextIndex: number,
) {
  const currentIndex = offerIDs.indexOf(offerID)
  if (currentIndex < 0) return offerIDs
  const boundedIndex = Math.max(0, Math.min(nextIndex, offerIDs.length - 1))
  const reordered = [...offerIDs]
  reordered.splice(currentIndex, 1)
  reordered.splice(boundedIndex, 0, offerID)
  return reordered
}

async function loadAllOffers() {
  const offers: MarketOffer[] = []
  let after = ''
  for (let page = 0; page < 20; page += 1) {
    const result = await api.marketOffers({ after, limit: 100 })
    offers.push(...result.offers)
    if (!result.next_after) break
    after = result.next_after
  }
  return [...new Map(offers.map((offer) => [offer.offer_id, offer])).values()]
}

export function APIKeyEditorPage() {
  const { keyID } = useParams()
  const editing = Boolean(keyID)
  const navigate = useNavigate()
  const draggedOffer = useRef<{ poolID: string; offerID: string } | null>(null)
  const [key, setKey] = useState<APIKey | null>(null)
  const [models, setModels] = useState<CatalogModel[]>([])
  const [offers, setOffers] = useState<MarketOffer[]>([])
  const [knownMembers, setKnownMembers] = useState<
    Record<string, APIKeyPoolMember>
  >({})
  const [displayName, setDisplayName] = useState('')
  const [pools, setPools] = useState<DraftPool[]>([])
  const [modelToAdd, setModelToAdd] = useState('')
  const [protocolToAdd, setProtocolToAdd] = useState<ChannelProtocol>(
    'openai_responses',
  )
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [conflict, setConflict] = useState<APIKeyConflictState | null>(null)
  const [error, setError] = useState('')
  const [offerError, setOfferError] = useState('')
  const blocker = useBlocker(dirty && !saving)

  useEffect(() => {
    if (blocker.state !== 'blocked') return
    if (window.confirm('放弃未保存的 API Key 配置？')) blocker.proceed()
    else blocker.reset()
  }, [blocker])

  useEffect(() => {
    let active = true
    setLoading(true)
    setKey(null)
    setModels([])
    setOffers([])
    setKnownMembers({})
    setDisplayName('')
    setPools([])
    setDirty(false)
    setConflict(null)
    setError('')
    setOfferError('')
    Promise.all([api.models(), keyID ? api.apiKey(keyID) : Promise.resolve(null)])
      .then(([catalog, existing]) => {
        if (!active) return
        setModels(catalog)
        setModelToAdd(catalog[0]?.id ?? '')
        if (existing) {
          setKey(existing)
          setDisplayName(existing.display_name)
          setKnownMembers(existingMemberMap(existing))
          setPools(draftPoolsFromKey(existing))
        }
      })
      .catch((caught) => {
        if (active) {
          setError(
            caught instanceof ApiError ? caught.message : 'API Key 配置加载失败',
          )
        }
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    void loadAllOffers()
      .then((marketOffers) => {
        if (active) setOffers(marketOffers)
      })
      .catch((caught) => {
        if (active) {
          setOfferError(
            caught instanceof ApiError ? caught.message : '市场报价加载失败',
          )
        }
      })
    return () => {
      active = false
    }
  }, [keyID])

  useEffect(() => {
    if (!models.some((model) => model.id === modelToAdd)) {
      setModelToAdd(models[0]?.id ?? '')
    }
  }, [modelToAdd, models])

  const poolCandidates = useMemo(
    () =>
      new Map(
        pools.map((pool) => [
          pool.draft_id,
          offers.filter(
            (offer) =>
              offer.model_id === pool.model_id &&
              offer.protocol === pool.protocol,
          ),
        ]),
      ),
    [offers, pools],
  )

  const markChanged = (change: () => void) => {
    change()
    setDirty(true)
  }

  const addPool = () => {
    if (!modelToAdd) return
    const id = draftID(modelToAdd, protocolToAdd)
    if (pools.some((pool) => pool.draft_id === id)) {
      setError('这个模型协议池已经存在')
      return
    }
    const firstOffer = offers.find(
      (offer) =>
        offer.model_id === modelToAdd && offer.protocol === protocolToAdd,
    )
    if (!firstOffer) {
      setError('市场中没有兼容报价')
      return
    }
    setError('')
    markChanged(() =>
      setPools((current) => [
        ...current,
        {
          draft_id: id,
          model_id: modelToAdd,
          protocol: protocolToAdd,
          offer_ids: [firstOffer.offer_id],
        },
      ]),
    )
  }

  const updatePool = (
    poolID: string,
    update: (pool: DraftPool) => DraftPool,
  ) => {
    markChanged(() =>
      setPools((current) =>
        current.map((pool) => (pool.draft_id === poolID ? update(pool) : pool)),
      ),
    )
  }

  const moveOffer = (poolID: string, offerID: string, nextIndex: number) => {
    updatePool(poolID, (pool) => {
      return {
        ...pool,
        offer_ids: moveOfferIDs(pool.offer_ids, offerID, nextIndex),
      }
    })
  }

  const validate = () => {
    if (!displayName.trim()) return '请输入 Key 名称'
    if (pools.length === 0) return '至少配置一个模型协议池'
    if (pools.some((pool) => pool.offer_ids.length === 0)) {
      return '每个模型协议池至少需要一个渠道'
    }
    return ''
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (conflict) return
    const validation = validate()
    if (validation) {
      setError(validation)
      return
    }
    setSaving(true)
    setError('')
    const input = pools.map(({ model_id, protocol, offer_ids }) => ({
      model_id,
      protocol,
      offer_ids,
    }))
    try {
      if (editing && keyID && key) {
        const updated = await api.updateAPIKey(
          keyID,
          key.version,
          displayName.trim(),
          input,
        )
        setKey(updated)
        setDirty(false)
        navigate(`/keys/${updated.id}`, { replace: true })
      } else {
        const created = await api.createAPIKey(displayName.trim(), input)
        setDirty(false)
        navigate(`/keys/${created.key.id}`, {
          replace: true,
          state: { secret: created.secret },
        })
      }
    } catch (caught) {
      if (
        editing &&
        keyID &&
		key &&
        caught instanceof ApiError &&
        caught.code === 'conflict'
      ) {
        try {
          const latest = await api.apiKey(keyID)
          setConflict({
            localVersion: key.version,
            serverVersion: latest.version,
          })
          setError('')
        } catch {
          setConflict({ localVersion: key.version, serverVersion: null })
          setError('无法读取服务器最新版本，请稍后重新加载')
        }
      } else {
        setError(
          caught instanceof ApiError ? caught.message : 'API Key 保存失败',
        )
      }
    } finally {
      setSaving(false)
    }
  }

  const reloadAfterConflict = async () => {
    if (!editing || !keyID || !conflict) return
    setSaving(true)
    setError('')
    try {
      const latest = await api.apiKey(keyID)
      setKey(latest)
      setDisplayName(latest.display_name)
      setKnownMembers(existingMemberMap(latest))
      setPools(draftPoolsFromKey(latest))
      setConflict(null)
      setDirty(false)
    } catch (caught) {
      setError(
        caught instanceof ApiError ? caught.message : '最新配置重新加载失败',
      )
    } finally {
      setSaving(false)
    }
  }

  if (loading) return <AppShell><LoadingState /></AppShell>

  return (
    <AppShell>
      <Link className="back-link" to={editing && keyID ? `/keys/${keyID}` : '/keys'}>
        ← API Key
      </Link>
      <header className="page-heading">
        <div>
          <h1>{editing ? '编辑 API Key' : '新建 API Key'}</h1>
        </div>
      </header>
      <InlineError>{error}</InlineError>
      {conflict && (
        <APIKeyConflictBanner
          busy={saving}
          conflict={conflict}
          onReload={() => void reloadAfterConflict()}
        />
      )}
      <form className="key-editor" onSubmit={(event) => void submit(event)}>
        <div className="key-editor-main">
          <section className="panel key-form-section">
            <TextField
              label="Key 名称"
              maxLength={80}
              onChange={(event) => {
                setDisplayName(event.target.value)
                setDirty(true)
              }}
              placeholder="例如：Cursor 主力 Key"
              value={displayName}
            />
          </section>
          <section className="panel key-pools-panel">
            <header className="panel-heading">
              <h2>模型协议池</h2>
              <span className="count-badge">{pools.length}</span>
            </header>
            <div className="pool-add-grid">
              <label className="field">
                <span className="field-label">模型</span>
                <select
                  className="input"
                  onChange={(event) => setModelToAdd(event.target.value)}
                  value={modelToAdd}
                >
                  {models.length === 0 && (
                    <option value="">管理员尚未配置模型目录</option>
                  )}
                  {models.map((model) => (
                    <option key={model.id} value={model.id}>
                      {model.provider} · {model.name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="field">
                <span className="field-label">API 格式</span>
                <select
                  className="input"
                  onChange={(event) =>
                    setProtocolToAdd(event.target.value as ChannelProtocol)
                  }
                  value={protocolToAdd}
                >
                  {protocols.map((protocol) => (
                    <option key={protocol} value={protocol}>
                      {protocolLabels[protocol]}
                    </option>
                  ))}
                </select>
              </label>
              <Button icon={<Icon name="plus" />} onClick={addPool} type="button">
                添加
              </Button>
            </div>
            <div className="key-pool-list">
              <InlineError>{offerError}</InlineError>
              {pools.map((pool) => {
                const model = models.find((item) => item.id === pool.model_id)
                const candidates = poolCandidates.get(pool.draft_id) ?? []
                const remaining = candidates.filter(
                  (offer) => !pool.offer_ids.includes(offer.offer_id),
                )
                return (
                  <article className="key-pool-card" key={pool.draft_id}>
                    <header>
                      <div>
                        <strong>{model?.name ?? pool.model_id}</strong>
                        <span>{protocolLabels[pool.protocol]}</span>
                      </div>
                      <Button
                        onClick={() =>
                          markChanged(() =>
                            setPools((current) =>
                              current.filter(
                                (item) => item.draft_id !== pool.draft_id,
                              ),
                            ),
                          )
                        }
                        type="button"
                        variant="quiet"
                      >
                        删除池
                      </Button>
                    </header>
                    <ol className="pool-candidate-list">
                      {pool.offer_ids.map((offerID, index) => {
                        const offer = candidates.find(
                          (item) => item.offer_id === offerID,
                        )
                        const known = knownMembers[offerID]
                        return (
                          <li
                            draggable
                            key={offerID}
                            onDragOver={(event) => event.preventDefault()}
                            onDragStart={() => {
                              draggedOffer.current = {
                                poolID: pool.draft_id,
                                offerID,
                              }
                            }}
                            onDrop={() => {
                              if (draggedOffer.current?.poolID === pool.draft_id) {
                                moveOffer(
                                  pool.draft_id,
                                  draggedOffer.current.offerID,
                                  index,
                                )
                              }
                              draggedOffer.current = null
                            }}
                          >
                            <span className="pool-priority">{index + 1}</span>
                            <span>
                              <strong>
                                {offer?.channel_display_name ??
                                  known?.channel_name ??
                                  offerID}
                              </strong>
                              <small>
                                {offer
                                  ? `${offer.owner_display_name} · ${offer.call_success_rate === null ? '暂无调用数据' : formatRate(offer.call_success_rate)}`
                                  : known
                                    ? `${known.provider_name} · ${
                                        known.eligible
                                          ? '可用'
                                          : known.ineligible_reason || '当前不可用'
                                      }`
                                    : '报价当前不在市场'}
                              </small>
                            </span>
                            <span className="pool-order-actions">
                              <button
                                aria-label="上移"
                                className="icon-button"
                                disabled={index === 0}
                                onClick={() =>
                                  moveOffer(pool.draft_id, offerID, index - 1)
                                }
                                type="button"
                              >
                                ↑
                              </button>
                              <button
                                aria-label="下移"
                                className="icon-button"
                                disabled={index === pool.offer_ids.length - 1}
                                onClick={() =>
                                  moveOffer(pool.draft_id, offerID, index + 1)
                                }
                                type="button"
                              >
                                ↓
                              </button>
                              <button
                                aria-label="移除渠道"
                                className="icon-button"
                                onClick={() =>
                                  updatePool(pool.draft_id, (current) => ({
                                    ...current,
                                    offer_ids: current.offer_ids.filter(
                                      (id) => id !== offerID,
                                    ),
                                  }))
                                }
                                type="button"
                              >
                                ×
                              </button>
                            </span>
                          </li>
                        )
                      })}
                    </ol>
                    {remaining.length > 0 && (
                      <label className="field pool-add-offer">
                        <span className="field-label">添加候选渠道</span>
                        <select
                          className="input"
                          onChange={(event) => {
                            const offerID = event.target.value
                            if (!offerID) return
                            updatePool(pool.draft_id, (current) => ({
                              ...current,
                              offer_ids: [...current.offer_ids, offerID],
                            }))
                            event.target.value = ''
                          }}
                          value=""
                        >
                          <option value="">选择渠道</option>
                          {remaining.map((offer) => (
                            <option key={offer.offer_id} value={offer.offer_id}>
                              {offer.channel_display_name} · {offer.owner_display_name}
                            </option>
                          ))}
                        </select>
                      </label>
                    )}
                  </article>
                )
              })}
              {pools.length === 0 && (
                <div className="empty-state">请选择模型和 API 格式</div>
              )}
            </div>
          </section>
        </div>
        <aside className="panel key-config-summary">
          <h2>配置摘要</h2>
          <dl className="detail-list">
            <div><dt>模型协议池</dt><dd>{pools.length}</dd></div>
            <div>
              <dt>候选渠道</dt>
              <dd>{pools.reduce((total, pool) => total + pool.offer_ids.length, 0)}</dd>
            </div>
            <div><dt>路由方式</dt><dd>优先级回退</dd></div>
          </dl>
          <div className="key-summary-actions">
            <Link className="button button-secondary" to={editing && keyID ? `/keys/${keyID}` : '/keys'}>
              取消
            </Link>
            <Button disabled={isAPIKeySaveBlocked(saving, conflict)} type="submit">
              {conflict ? '保存被阻止' : saving ? '正在保存' : editing ? '保存配置' : '创建 Key'}
            </Button>
          </div>
        </aside>
      </form>
    </AppShell>
  )
}
