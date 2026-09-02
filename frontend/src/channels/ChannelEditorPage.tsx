import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { Link, useBlocker, useNavigate, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { CatalogModel, Channel, ChannelOffer, ChannelProtocol } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState, TextField } from '../ui/FormControls'
import { Icon } from '../ui/Icon'
import { protocolLabels } from './presentation'

const protocols: ChannelProtocol[] = [
  'openai_chat_completions',
  'openai_responses',
  'anthropic_messages',
  'google_gemini_generate_content',
]

export type OfferDraft = {
  id?: string
  selected: boolean
  selectionTouched: boolean
  upstreamModelID: string
  upstreamTouched: boolean
}

export type ModelGroup = {
  modelID: string
  modelName: string
  provider: string
  multiplier: string
  multiplierTouched: boolean
  offers: Record<ChannelProtocol, OfferDraft>
}

export type ChannelFieldDraft = {
  displayName: string
  displayNameTouched: boolean
  baseURL: string
  baseURLTouched: boolean
}

function emptyOffers(modelID: string): Record<ChannelProtocol, OfferDraft> {
  return Object.fromEntries(protocols.map((protocol) => [protocol, {
    selected: false,
    selectionTouched: false,
    upstreamModelID: modelID,
    upstreamTouched: false,
  }])) as Record<ChannelProtocol, OfferDraft>
}

export function channelGroups(channel: Channel): ModelGroup[] {
  const groups = new Map<string, ModelGroup>()
  for (const offer of channel.offers.filter((item) => item.status !== 'deleted')) {
    const group = groups.get(offer.model_id) ?? {
      modelID: offer.model_id,
      modelName: offer.model_name,
      provider: offer.model_provider,
      multiplier: offer.multiplier,
      multiplierTouched: false,
      offers: emptyOffers(offer.model_id),
    }
    group.offers[offer.protocol] = {
      id: offer.id,
      selected: true,
      selectionTouched: false,
      upstreamModelID: offer.upstream_model_id ?? offer.model_id,
      upstreamTouched: false,
    }
    groups.set(offer.model_id, group)
  }
  return [...groups.values()]
}

function offerChanged(current: ChannelOffer, desired: OfferDraft, multiplier: string) {
  return current.upstream_model_id !== desired.upstreamModelID || current.multiplier !== multiplier
}

export function rebaseGroups(drafts: ModelGroup[], latest: Channel): ModelGroup[] {
  return drafts.map((group) => {
    const currentOffers = latest.offers.filter((offer) =>
      offer.status !== 'deleted' && offer.model_id === group.modelID)
    const currentGroup = currentOffers[0]
    return {
      ...group,
      modelName: currentGroup?.model_name ?? group.modelName,
      provider: currentGroup?.model_provider ?? group.provider,
      multiplier: group.multiplierTouched
        ? group.multiplier
        : currentGroup?.multiplier ?? group.multiplier,
      offers: Object.fromEntries(protocols.map((protocol) => {
        const draft = group.offers[protocol]
        const current = currentOffers.find((offer) => offer.protocol === protocol)
        if (!current) {
          return [protocol, {
            ...draft,
            id: undefined,
            selected: draft.selectionTouched ? draft.selected : false,
          }]
        }
        return [protocol, {
          ...draft,
          id: current.id,
          selected: draft.selectionTouched ? draft.selected : true,
          upstreamModelID: draft.upstreamTouched
            ? draft.upstreamModelID
            : current.upstream_model_id ?? current.model_id,
        }]
      })) as Record<ChannelProtocol, OfferDraft>,
    }
  })
}

export function rebaseChannelFields(draft: ChannelFieldDraft, latest: Channel): ChannelFieldDraft {
  return {
    displayName: draft.displayNameTouched ? draft.displayName : latest.display_name,
    displayNameTouched: draft.displayNameTouched,
    baseURL: draft.baseURLTouched ? draft.baseURL : latest.base_url ?? '',
    baseURLTouched: draft.baseURLTouched,
  }
}

export function ChannelEditorPage() {
  const { channelID } = useParams()
  const editing = Boolean(channelID)
  const routeKey = channelID ?? 'new'
  const routeKeyRef = useRef(routeKey)
  routeKeyRef.current = routeKey
  const navigate = useNavigate()
  const [models, setModels] = useState<CatalogModel[]>([])
  const [channel, setChannel] = useState<Channel | null>(null)
  const [displayName, setDisplayName] = useState('')
  const [displayNameTouched, setDisplayNameTouched] = useState(false)
  const [baseURL, setBaseURL] = useState('')
  const [baseURLTouched, setBaseURLTouched] = useState(false)
  const [credential, setCredential] = useState('')
  const [groups, setGroups] = useState<ModelGroup[]>([])
  const [modelToAdd, setModelToAdd] = useState('')
  const [loadedRouteKey, setLoadedRouteKey] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState('')
  const blocker = useBlocker(dirty && !saving)

  useEffect(() => {
    if (blocker.state !== 'blocked') return
    if (window.confirm('放弃未保存的渠道配置？')) blocker.proceed()
    else blocker.reset()
  }, [blocker])

  useEffect(() => {
    let active = true
    setLoading(true)
    setSaving(false)
    setDirty(false)
    setChannel(null)
    setDisplayName('')
    setDisplayNameTouched(false)
    setBaseURL('')
    setBaseURLTouched(false)
    setCredential('')
    setGroups([])
    setModels([])
    setModelToAdd('')
    setError('')
    Promise.all([
      api.models(),
      channelID ? api.channel(channelID) : Promise.resolve(null),
    ]).then(([catalog, existing]) => {
      if (!active) return
      setModels(catalog)
      if (existing) {
        setChannel(existing)
        setDisplayName(existing.display_name)
        setBaseURL(existing.base_url ?? '')
        setGroups(channelGroups(existing))
      }
      setModelToAdd(catalog[0]?.id ?? '')
    }).catch((caught) => {
      if (active) setError(caught instanceof ApiError ? caught.message : '渠道配置加载失败')
    }).finally(() => {
      if (!active) return
      setLoadedRouteKey(routeKey)
      setLoading(false)
    })
    return () => { active = false }
  }, [channelID, routeKey])

  const availableModels = useMemo(
    () => models.filter((model) => !groups.some((group) => group.modelID === model.id)),
    [groups, models],
  )

  useEffect(() => {
    if (!availableModels.some((model) => model.id === modelToAdd)) {
      setModelToAdd(availableModels[0]?.id ?? '')
    }
  }, [availableModels, modelToAdd])

  const change = (action: () => void) => {
    action()
    setDirty(true)
  }

  const addModel = () => {
    const model = models.find((item) => item.id === modelToAdd)
    if (!model) return
    change(() => setGroups((items) => [...items, {
      modelID: model.id,
      modelName: model.name,
      provider: model.provider,
      multiplier: '1',
      multiplierTouched: false,
      offers: emptyOffers(model.id),
    }]))
  }

  const updateGroup = (modelID: string, update: (group: ModelGroup) => ModelGroup) => {
    change(() => setGroups((items) => items.map((group) => group.modelID === modelID ? update(group) : group)))
  }

  const validate = () => {
    if (!displayName.trim()) return '请输入渠道名称'
    if (!baseURL.trim()) return '请输入 Base URL'
    if (!editing && !credential) return '请输入上游 API Key'
    const selected = groups.flatMap((group) => protocols.filter((protocol) => group.offers[protocol].selected).map((protocol) => ({ group, protocol })))
    if (selected.length === 0) return '至少启用一个模型协议'
    for (const { group, protocol } of selected) {
      const multiplier = Number(group.multiplier)
      if (!Number.isFinite(multiplier) || multiplier < 0 || multiplier > 1000) return `${group.modelName} 的倍率无效`
      if (!group.offers[protocol].upstreamModelID.trim()) return `${group.modelName} 缺少上游模型 ID`
    }
    return ''
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const validation = validate()
    if (validation) {
      setError(validation)
      return
    }
    setSaving(true)
    setError('')
    const submittedRouteKey = routeKey
    const isCurrentRoute = () => routeKeyRef.current === submittedRouteKey
    try {
      if (!editing) {
        const created = await api.createChannel({
          display_name: displayName.trim(),
          base_url: baseURL.trim(),
          credential,
          offers: groups.flatMap((group) => protocols
            .filter((protocol) => group.offers[protocol].selected)
            .map((protocol) => ({
              model_id: group.modelID,
              protocol,
              upstream_model_id: group.offers[protocol].upstreamModelID.trim(),
              multiplier: group.multiplier,
            }))),
        })
        if (!isCurrentRoute()) return
        setDirty(false)
        navigate(`/channels/${created.id}`, { replace: true })
        return
      }
      if (!channel || !channelID || channel.id !== channelID) return
      let latest = channel
      if (displayName.trim() !== channel.display_name || baseURL.trim() !== channel.base_url || credential) {
        latest = await api.updateChannel(channelID, {
          display_name: displayName.trim(),
          base_url: baseURL.trim(),
          expected_version: latest.version,
          ...(credential ? { credential } : {}),
        })
        if (!isCurrentRoute()) return
      }

      for (const group of groups) {
        for (const protocol of protocols) {
          if (!isCurrentRoute()) return
          const desired = group.offers[protocol]
          if (!desired.id || !desired.selected) continue
          const current = latest.offers.find((offer) => offer.id === desired.id)
          if (!current || !offerChanged(current, desired, group.multiplier)) continue
          await api.updateChannelOffer(current.id, current.version ?? 0, desired.upstreamModelID.trim(), group.multiplier)
          if (!isCurrentRoute()) return
          latest = await api.channel(channelID)
        }
      }
      for (const group of groups) {
        for (const protocol of protocols) {
          if (!isCurrentRoute()) return
          const desired = group.offers[protocol]
          if (!desired.id || desired.selected) continue
          const current = latest.offers.find((offer) => offer.id === desired.id)
          if (!current || current.status === 'deleted') continue
          await api.deleteChannelOffer(current.id, current.version ?? 0)
          if (!isCurrentRoute()) return
          latest = await api.channel(channelID)
        }
      }
      for (const group of groups) {
        for (const protocol of protocols) {
          if (!isCurrentRoute()) return
          const desired = group.offers[protocol]
          if (desired.id || !desired.selected) continue
          await api.addChannelOffer(channelID, latest.version, {
            model_id: group.modelID,
            protocol,
            upstream_model_id: desired.upstreamModelID.trim(),
            multiplier: group.multiplier,
          })
          if (!isCurrentRoute()) return
          latest = await api.channel(channelID)
        }
      }
      setDirty(false)
      navigate(`/channels/${channelID}`, { replace: true })
    } catch (caught) {
      if (!isCurrentRoute()) return
      if (caught instanceof ApiError && caught.code === 'conflict' && channelID) {
        try {
          const latest = await api.channel(channelID)
          if (!isCurrentRoute()) return
          const rebasedFields = rebaseChannelFields({
            displayName,
            displayNameTouched,
            baseURL,
            baseURLTouched,
          }, latest)
          setChannel(latest)
          setDisplayName(rebasedFields.displayName)
          setDisplayNameTouched(rebasedFields.displayNameTouched)
          setBaseURL(rebasedFields.baseURL)
          setBaseURLTouched(rebasedFields.baseURLTouched)
          setGroups((current) => rebaseGroups(current, latest))
          setError('配置已更新到最新版本，你的输入已保留。请检查后再次保存。')
        } catch {
          setError('配置已被其他操作更新，最新版本加载失败；你的输入仍保留在当前页面。')
        }
      } else {
        setError(caught instanceof ApiError ? caught.message : '渠道保存失败')
      }
    } finally {
      if (isCurrentRoute()) setSaving(false)
    }
  }

  if (loading || loadedRouteKey !== routeKey) return <AppShell><LoadingState /></AppShell>
  if (editing && (!channel || channel.id !== channelID)) {
    return <AppShell><InlineError>{error || '渠道不存在'}</InlineError></AppShell>
  }

  return (
    <AppShell>
      <Link className="back-link" to={channelID ? `/channels/${channelID}` : '/channels'}>← 返回</Link>
      <header className="page-heading"><div><h1>{editing ? '编辑渠道' : '上架渠道'}</h1></div></header>
      <InlineError>{error}</InlineError>
      <form className="channel-editor" onSubmit={submit}>
        <section className="panel channel-config-panel">
          <header className="panel-heading"><h2>连接</h2></header>
          <div className="channel-form-section">
            <div className="field-row">
              <TextField label="渠道名称" maxLength={80} onChange={(event) => change(() => { setDisplayName(event.target.value); setDisplayNameTouched(true) })} required value={displayName} />
              <TextField label="Base URL" onChange={(event) => change(() => { setBaseURL(event.target.value); setBaseURLTouched(true) })} placeholder="https://gateway.example.com" required type="url" value={baseURL} />
            </div>
            <TextField
              autoComplete="off"
              hint={editing ? '留空则保持现有凭据' : undefined}
              label={editing ? '替换上游 API Key' : '上游 API Key'}
              onChange={(event) => change(() => setCredential(event.target.value))}
              required={!editing}
              type="password"
              value={credential}
            />
          </div>
        </section>

        <section className="panel channel-model-panel">
          <header className="panel-heading">
            <h2>模型与协议</h2>
            <div className="add-model-control">
              <label className="visually-hidden" htmlFor="channel-model-add">模型</label>
              <select className="input" id="channel-model-add" onChange={(event) => setModelToAdd(event.target.value)} value={modelToAdd}>
                {availableModels.map((model) => <option key={model.id} value={model.id}>{model.provider} · {model.name}</option>)}
              </select>
              <Button disabled={!modelToAdd} onClick={addModel} type="button" variant="secondary"><Icon name="plus" />添加模型</Button>
            </div>
          </header>
          <div className="model-config-list">
            {groups.map((group) => {
              const canRemove = !protocols.some((protocol) => group.offers[protocol].id)
              return (
                <article className="model-config-card" key={group.modelID}>
                  <header>
                    <div><strong>{group.modelName}</strong><span>{group.provider} · {group.modelID}</span></div>
                    <div className="model-multiplier">
                      <TextField label="价格倍率" min="0" onChange={(event) => updateGroup(group.modelID, (current) => ({ ...current, multiplier: event.target.value, multiplierTouched: true }))} required step="0.000000001" type="number" value={group.multiplier} />
                      {canRemove && <Button onClick={() => change(() => setGroups((items) => items.filter((item) => item.modelID !== group.modelID)))} type="button" variant="quiet">移除</Button>}
                    </div>
                  </header>
                  <div className="protocol-config-grid">
                    {protocols.map((protocol) => {
                      const draft = group.offers[protocol]
                      return (
                        <div className={`protocol-config ${draft.selected ? 'protocol-config-selected' : ''}`} key={protocol}>
                          <label className="protocol-toggle">
                            <input
                              checked={draft.selected}
                              onChange={(event) => updateGroup(group.modelID, (current) => ({
                                ...current,
                                offers: { ...current.offers, [protocol]: { ...current.offers[protocol], selected: event.target.checked, selectionTouched: true } },
                              }))}
                              type="checkbox"
                            />
                            <span>{protocolLabels[protocol]}</span>
                          </label>
                          <TextField
                            disabled={!draft.selected}
                            label="上游模型 ID"
                            onChange={(event) => updateGroup(group.modelID, (current) => ({
                              ...current,
                              offers: { ...current.offers, [protocol]: { ...current.offers[protocol], upstreamModelID: event.target.value, upstreamTouched: true } },
                            }))}
                            required={draft.selected}
                            value={draft.upstreamModelID}
                          />
                        </div>
                      )
                    })}
                  </div>
                </article>
              )
            })}
            {groups.length === 0 && <div className="empty-state">添加模型后配置协议</div>}
          </div>
        </section>
        <div className="form-actions channel-editor-actions">
          <Link className="button button-secondary" to={channelID ? `/channels/${channelID}` : '/channels'}>取消</Link>
          <Button disabled={saving} type="submit">{saving ? '正在保存' : editing ? '保存配置' : '创建草稿'}</Button>
        </div>
      </form>
    </AppShell>
  )
}
