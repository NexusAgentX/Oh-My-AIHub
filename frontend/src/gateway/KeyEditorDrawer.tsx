import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { api, ApiError } from '../api/client'
import type { APIKey, ChannelProtocol } from '../api/contracts'
import { errorMessage } from '../api/query'
import {
  useAllMarketOffersQuery,
  useCatalogModelsQuery,
} from '../channels/marketQueries'
import {
  Badge,
  Button,
  Drawer,
  EmptyState,
  Icon,
  InlineError,
  LoadingState,
  Notice,
  SelectField,
  TextField,
} from '../ui'
import { protocolLabels, protocols, formatRate } from './presentation'
import { gatewayKeys, useCreateKeyMutation, useUpdateKeyMutation } from './queries'
import {
  draftID,
  draftRoutesFromKey,
  existingMemberMap,
  isAPIKeySaveBlocked,
  moveOfferIDs,
  routeInputs,
  validateKeyDraft,
  type APIKeyConflictState,
  type DraftRoute,
} from './routeDraft'

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
    <Notice
      action={
        <Button disabled={busy} onClick={onReload} size="sm" type="button" variant="secondary">
          {busy ? '正在重新加载' : '重新加载最新版本'}
        </Button>
      }
      tone="danger"
    >
      服务器版本 v{conflict.serverVersion ?? '未知'}，本地草稿基于 v{conflict.localVersion}
      。草稿仍保留；重新加载前不能保存。
    </Notice>
  )
}

type EditorState = { dirty: boolean; saving: boolean }

/**
 * Key 设置抽屉：创建或编辑名称与路由（含备用顺序）。
 * 编辑时用 expected_version 做乐观并发，冲突时保留草稿并要求显式重新加载。
 */
export function KeyEditorDrawer({
  open,
  keyData,
  onClose,
  onSaved,
}: {
  open: boolean
  /** 传入则为编辑，否则为创建 */
  keyData?: APIKey | null
  onClose: () => void
  onSaved: (result: { key: APIKey; secret: string }) => void
}) {
  const [state, setState] = useState<EditorState>({ dirty: false, saving: false })
  const requestClose = () => {
    if (state.saving) return
    if (state.dirty && !window.confirm('放弃未保存的 Key 设置？')) return
    onClose()
  }
  return (
    <Drawer
      busy={state.saving}
      description={keyData ? undefined : '命名并配置至少一个路由'}
      onClose={requestClose}
      open={open}
      title={keyData ? 'Key 设置' : '新建 API Key'}
    >
      <KeyEditorForm
        keyData={keyData ?? null}
        onCancel={requestClose}
        onSaved={onSaved}
        onStateChange={setState}
      />
    </Drawer>
  )
}

function KeyEditorForm({
  keyData,
  onCancel,
  onSaved,
  onStateChange,
}: {
  keyData: APIKey | null
  onCancel: () => void
  onSaved: (result: { key: APIKey; secret: string }) => void
  onStateChange: (state: EditorState) => void
}) {
  const client = useQueryClient()
  const models = useCatalogModelsQuery()
  const offersQuery = useAllMarketOffersQuery()
  const create = useCreateKeyMutation()
  const update = useUpdateKeyMutation()
  const dragged = useRef<{ routeID: string; offerID: string } | null>(null)

  const [base, setBase] = useState<APIKey | null>(keyData)
  const [displayName, setDisplayName] = useState(keyData?.display_name ?? '')
  const [routes, setRoutes] = useState<DraftRoute[]>(
    keyData ? draftRoutesFromKey(keyData) : [],
  )
  const [known, setKnown] = useState(keyData ? existingMemberMap(keyData) : {})
  const [modelToAdd, setModelToAdd] = useState('')
  const [protocolToAdd, setProtocolToAdd] = useState<ChannelProtocol>('openai_chat_completions')
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [conflict, setConflict] = useState<APIKeyConflictState | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    onStateChange({ dirty, saving })
  }, [dirty, saving, onStateChange])

  const modelList = models.data ?? []
  const selectedModel = modelList.some((model) => model.id === modelToAdd)
    ? modelToAdd
    : (modelList[0]?.id ?? '')
  const offers = useMemo(() => offersQuery.data ?? [], [offersQuery.data])

  const change = (apply: () => void) => {
    apply()
    setDirty(true)
  }

  const addRoute = () => {
    if (!selectedModel) return
    const id = draftID(selectedModel, protocolToAdd)
    if (routes.some((route) => route.draft_id === id)) {
      setError('这个路由已经存在')
      return
    }
    const first = offers.find(
      (offer) => offer.model_id === selectedModel && offer.protocol === protocolToAdd,
    )
    if (!first) {
      setError('市场中没有兼容报价')
      return
    }
    setError('')
    change(() =>
      setRoutes((current) => [
        ...current,
        {
          draft_id: id,
          model_id: selectedModel,
          protocol: protocolToAdd,
          offer_ids: [first.offer_id],
        },
      ]),
    )
  }

  const updateRoute = (routeID: string, apply: (route: DraftRoute) => DraftRoute) =>
    change(() =>
      setRoutes((current) =>
        current.map((route) => (route.draft_id === routeID ? apply(route) : route)),
      ),
    )

  const moveOffer = (routeID: string, offerID: string, index: number) =>
    updateRoute(routeID, (route) => ({
      ...route,
      offer_ids: moveOfferIDs(route.offer_ids, offerID, index),
    }))

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (conflict) return
    const invalid = validateKeyDraft(displayName, routes)
    if (invalid) {
      setError(invalid)
      return
    }
    setSaving(true)
    setError('')
    const pools = routeInputs(routes)
    try {
      if (base) {
        const key = await update.mutateAsync({
          keyID: base.id,
          version: base.version,
          displayName: displayName.trim(),
          pools,
        })
        setDirty(false)
        onSaved({ key, secret: '' })
      } else {
        const created = await create.mutateAsync({ displayName: displayName.trim(), pools })
        setDirty(false)
        onSaved(created)
      }
    } catch (caught) {
      if (base && caught instanceof ApiError && caught.code === 'conflict') {
        try {
          const latest = await api.apiKey(base.id)
          setConflict({ localVersion: base.version, serverVersion: latest.version })
        } catch {
          setConflict({ localVersion: base.version, serverVersion: null })
          setError('无法读取服务器最新版本，请稍后重新加载')
        }
      } else {
        setError(errorMessage(caught, 'Key 保存失败'))
      }
    } finally {
      setSaving(false)
    }
  }

  const reload = async () => {
    if (!base || !conflict) return
    setSaving(true)
    setError('')
    try {
      const latest = await api.apiKey(base.id)
      client.setQueryData(gatewayKeys.key(base.id), latest)
      setBase(latest)
      setDisplayName(latest.display_name)
      setRoutes(draftRoutesFromKey(latest))
      setKnown(existingMemberMap(latest))
      setConflict(null)
      setDirty(false)
    } catch (caught) {
      setError(errorMessage(caught, '最新配置重新加载失败'))
    } finally {
      setSaving(false)
    }
  }

  if (models.isPending) return <LoadingState />

  return (
    <form className="key-editor" onSubmit={(event) => void submit(event)}>
      <div className="key-editor-body">
        <InlineError>{error}</InlineError>
        {conflict && (
          <APIKeyConflictBanner busy={saving} conflict={conflict} onReload={() => void reload()} />
        )}
        <TextField
          label="Key 名称"
          maxLength={80}
          onChange={(event) => change(() => setDisplayName(event.target.value))}
          placeholder="例如：Cursor 主力 Key"
          value={displayName}
        />

        <section aria-label="路由" className="route-editor">
          <h3 className="section-title">
            路由 <Badge>{routes.length}</Badge>
          </h3>
          <div className="route-add">
            <SelectField
              label="模型"
              onChange={(event) => setModelToAdd(event.target.value)}
              value={selectedModel}
            >
              {modelList.length === 0 && <option value="">模型目录为空</option>}
              {modelList.map((model) => (
                <option key={model.id} value={model.id}>
                  {model.provider} · {model.name}
                </option>
              ))}
            </SelectField>
            <SelectField
              label="API 格式"
              onChange={(event) => setProtocolToAdd(event.target.value as ChannelProtocol)}
              value={protocolToAdd}
            >
              {protocols.map((protocol) => (
                <option key={protocol} value={protocol}>
                  {protocolLabels[protocol]}
                </option>
              ))}
            </SelectField>
            <Button
              disabled={!selectedModel || offersQuery.isPending}
              icon={<Icon name="plus" />}
              onClick={addRoute}
              type="button"
              variant="secondary"
            >
              添加路由
            </Button>
          </div>
          {offersQuery.isError && (
            <InlineError>{errorMessage(offersQuery.error, '市场报价加载失败')}</InlineError>
          )}

          {routes.length === 0 ? (
            <EmptyState title="还没有路由" description="选择模型和 API 格式后添加。" />
          ) : (
            <div className="route-list">
              {routes.map((route) => {
                const model = modelList.find((item) => item.id === route.model_id)
                const candidates = offers.filter(
                  (offer) =>
                    offer.model_id === route.model_id && offer.protocol === route.protocol,
                )
                const remaining = candidates.filter(
                  (offer) => !route.offer_ids.includes(offer.offer_id),
                )
                return (
                  <article className="route-card" key={route.draft_id}>
                    <header>
                      <div>
                        <strong>{model?.name ?? route.model_id}</strong>
                        <span>{protocolLabels[route.protocol]}</span>
                      </div>
                      <Button
                        onClick={() =>
                          change(() =>
                            setRoutes((current) =>
                              current.filter((item) => item.draft_id !== route.draft_id),
                            ),
                          )
                        }
                        size="sm"
                        type="button"
                        variant="quiet"
                      >
                        删除路由
                      </Button>
                    </header>
                    <p className="route-order-label">备用顺序</p>
                    <ol className="route-members route-members-editable">
                      {route.offer_ids.map((offerID, index) => {
                        const offer = candidates.find((item) => item.offer_id === offerID)
                        const existing = known[offerID]
                        return (
                          <li
                            draggable
                            key={offerID}
                            onDragOver={(event) => event.preventDefault()}
                            onDragStart={() => {
                              dragged.current = { routeID: route.draft_id, offerID }
                            }}
                            onDrop={() => {
                              if (dragged.current?.routeID === route.draft_id) {
                                moveOffer(route.draft_id, dragged.current.offerID, index)
                              }
                              dragged.current = null
                            }}
                          >
                            <span className="route-rank num">{index + 1}</span>
                            <span className="route-member-copy">
                              <strong>
                                {offer?.channel_display_name ?? existing?.channel_name ?? offerID}
                              </strong>
                              <small>
                                {offer
                                  ? `${offer.owner_display_name} · ${offer.call_success_rate === null ? '暂无调用数据' : formatRate(offer.call_success_rate)}`
                                  : existing
                                    ? `${existing.provider_name} · ${existing.eligible ? '可用' : '需更新'}`
                                    : '报价当前不在市场'}
                              </small>
                            </span>
                            <span className="route-member-actions">
                              <button
                                aria-label="上移"
                                className="icon-button"
                                disabled={index === 0}
                                onClick={() => moveOffer(route.draft_id, offerID, index - 1)}
                                type="button"
                              >
                                ↑
                              </button>
                              <button
                                aria-label="下移"
                                className="icon-button"
                                disabled={index === route.offer_ids.length - 1}
                                onClick={() => moveOffer(route.draft_id, offerID, index + 1)}
                                type="button"
                              >
                                ↓
                              </button>
                              <button
                                aria-label="移除渠道"
                                className="icon-button"
                                onClick={() =>
                                  updateRoute(route.draft_id, (current) => ({
                                    ...current,
                                    offer_ids: current.offer_ids.filter((id) => id !== offerID),
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
                      <SelectField
                        className="route-add-offer"
                        label="添加渠道"
                        onChange={(event) => {
                          const offerID = event.target.value
                          if (!offerID) return
                          updateRoute(route.draft_id, (current) => ({
                            ...current,
                            offer_ids: [...current.offer_ids, offerID],
                          }))
                        }}
                        value=""
                      >
                        <option value="">选择渠道</option>
                        {remaining.map((offer) => (
                          <option key={offer.offer_id} value={offer.offer_id}>
                            {offer.channel_display_name} · {offer.owner_display_name}
                          </option>
                        ))}
                      </SelectField>
                    )}
                  </article>
                )
              })}
            </div>
          )}
        </section>
      </div>
      <div className="drawer-actions">
        <Button onClick={onCancel} type="button" variant="secondary">
          取消
        </Button>
        <Button
          disabled={isAPIKeySaveBlocked(saving, conflict)}
          loading={saving}
          type="submit"
        >
          {conflict ? '保存被阻止' : base ? '保存' : '创建 Key'}
        </Button>
      </div>
    </form>
  )
}

