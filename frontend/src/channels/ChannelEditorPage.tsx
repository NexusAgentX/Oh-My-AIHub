import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { Link, useBlocker, useNavigate, useParams } from 'react-router-dom'
import type { CatalogModel, Channel, ChannelProtocol } from '../api/types'
import { errorMessage } from '../api/query'
import {
  Button,
  ButtonLink,
  Card,
  Checkbox,
  EmptyState,
  Icon,
  InlineError,
  PageHeader,
  PasswordField,
  QueryBoundary,
  SelectField,
  TextField,
} from '../ui'
import {
  channelGroups,
  emptyOffers,
  isConflict,
  protocols,
  rebaseChannelFields,
  rebaseGroups,
  validateDraft,
  type ModelGroup,
  type OfferDraft,
} from './editorModel'
import { ConfirmActionDialog, protocolLabels } from './presentation'
import {
  useCatalogModelsQuery,
  useChannelQuery,
  useCreateChannelMutation,
  useLoadLatestChannel,
  useUpdateChannelMutation,
} from './queries'

export function ChannelEditorPage() {
  const { channelID = '' } = useParams()
  const catalog = useCatalogModelsQuery()
  const channelQuery = useChannelQuery(channelID)

  return (
    <QueryBoundary errorFallback="渠道配置加载失败" query={catalog}>
      {(models) => channelID ? (
        <QueryBoundary errorFallback="渠道配置加载失败" query={channelQuery}>
          {(channel) => <ChannelEditorForm channel={channel} key={channel.id} models={models} />}
        </QueryBoundary>
      ) : (
        <ChannelEditorForm key="new" models={models} />
      )}
    </QueryBoundary>
  )
}

function ChannelEditorForm({ models, channel: initial }: { models: CatalogModel[]; channel?: Channel }) {
  const editing = Boolean(initial)
  const navigate = useNavigate()
  const createChannel = useCreateChannelMutation()
  const updateChannel = useUpdateChannelMutation()
  const loadLatest = useLoadLatestChannel()
  const saving = createChannel.isPending || updateChannel.isPending
  const mounted = useRef(true)
  const leaveAllowed = useRef(false)

  const [channel, setChannel] = useState<Channel | undefined>(initial)
  const [displayName, setDisplayName] = useState(initial?.display_name ?? '')
  const [displayNameTouched, setDisplayNameTouched] = useState(false)
  const [baseURL, setBaseURL] = useState(initial?.base_url ?? '')
  const [baseURLTouched, setBaseURLTouched] = useState(false)
  const [credential, setCredential] = useState('')
  const [groups, setGroups] = useState<ModelGroup[]>(() => initial ? channelGroups(initial) : [])
  const [modelToAdd, setModelToAdd] = useState('')
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  const blocker = useBlocker(() => dirty && !leaveAllowed.current)

  const availableModels = useMemo(
    () => models.filter((model) => !groups.some((group) => group.modelID === model.id)),
    [groups, models],
  )
  const selectedModel = availableModels.some((model) => model.id === modelToAdd)
    ? modelToAdd
    : availableModels[0]?.id ?? ''

  const change = (action: () => void) => {
    action()
    setDirty(true)
  }

  const addModel = () => {
    const model = models.find((item) => item.id === selectedModel)
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

  const updateOffer = (modelID: string, protocol: ChannelProtocol, patch: Partial<OfferDraft>) => {
    updateGroup(modelID, (group) => ({
      ...group,
      offers: { ...group.offers, [protocol]: { ...group.offers[protocol], ...patch } },
    }))
  }

  const leave = (path: string) => {
    leaveAllowed.current = true
    navigate(path, { replace: true })
  }

  const rebaseOnConflict = async (channelID: string) => {
    try {
      const latest = await loadLatest(channelID)
      if (!mounted.current) return
      const fields = rebaseChannelFields({ displayName, displayNameTouched, baseURL, baseURLTouched }, latest)
      setChannel(latest)
      setDisplayName(fields.displayName)
      setBaseURL(fields.baseURL)
      setGroups((current) => rebaseGroups(current, latest))
      setError('配置已更新到最新版本，你的输入已保留。请检查后再次保存。')
    } catch {
      if (mounted.current) setError('配置已被其他操作更新，最新版本加载失败；你的输入仍保留在当前页面。')
    }
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const draft = { displayName, baseURL, credential, groups }
    const invalid = validateDraft(draft, editing)
    if (invalid) {
      setError(invalid)
      return
    }
    setError('')
    try {
      if (!channel) {
        const created = await createChannel.mutateAsync(draft)
        if (mounted.current) leave(`/channels/${created.id}`)
        return
      }
      await updateChannel.mutateAsync({ channel, draft, isActive: () => mounted.current })
      if (mounted.current) leave(`/channels/${channel.id}`)
    } catch (caught) {
      if (!mounted.current) return
      if (channel && isConflict(caught)) await rebaseOnConflict(channel.id)
      else setError(errorMessage(caught, '渠道保存失败'))
    }
  }

  const backTo = channel ? `/channels/${channel.id}` : '/channels'

  return (
    <>
      <PageHeader
        back={<Link className="back-link" to={backTo}>← 返回</Link>}
        title={editing ? '编辑渠道' : '上架渠道'}
      />
      <form className="sharing-editor" onSubmit={submit}>
        <Card title="连接">
          <div className="sharing-fields">
            <TextField
              label="渠道名称"
              maxLength={80}
              onChange={(event) => change(() => { setDisplayName(event.target.value); setDisplayNameTouched(true) })}
              required
              value={displayName}
            />
            <TextField
              label="Base URL"
              onChange={(event) => change(() => { setBaseURL(event.target.value); setBaseURLTouched(true) })}
              placeholder="https://gateway.example.com"
              required
              type="url"
              value={baseURL}
            />
            <PasswordField
              autoComplete="new-password"
              hint={editing ? '留空则保持现有凭据' : undefined}
              label={editing ? '替换上游 API Key' : '上游 API Key'}
              onChange={(event) => change(() => setCredential(event.target.value))}
              required={!editing}
              value={credential}
            />
          </div>
        </Card>

        <Card title="模型与协议">
          <div className="sharing-add-model">
            <SelectField
              disabled={availableModels.length === 0}
              label="添加模型"
              onChange={(event) => setModelToAdd(event.target.value)}
              value={selectedModel}
            >
              {availableModels.map((model) => <option key={model.id} value={model.id}>{model.provider} · {model.name}</option>)}
            </SelectField>
            <Button disabled={!selectedModel} icon={<Icon name="plus" />} onClick={addModel} type="button" variant="secondary">添加</Button>
          </div>
          {groups.length === 0 ? (
            <EmptyState title="添加模型后配置协议" />
          ) : (
            <div className="sharing-models">
              {groups.map((group) => (
                <ModelCard
                  group={group}
                  key={group.modelID}
                  onMultiplier={(value) => updateGroup(group.modelID, (current) => ({ ...current, multiplier: value, multiplierTouched: true }))}
                  onOffer={(protocol, patch) => updateOffer(group.modelID, protocol, patch)}
                  onRemove={() => change(() => setGroups((items) => items.filter((item) => item.modelID !== group.modelID)))}
                />
              ))}
            </div>
          )}
        </Card>

        <div className="sharing-editor-actions">
          <InlineError>{error}</InlineError>
          <div className="sharing-editor-buttons">
            <ButtonLink to={backTo}>取消</ButtonLink>
            <Button loading={saving} type="submit">{editing ? '保存配置' : '创建草稿'}</Button>
          </div>
        </div>
      </form>

      <ConfirmActionDialog
        cancelLabel="继续编辑"
        confirmLabel="放弃修改"
        danger
        onCancel={() => blocker.reset?.()}
        onConfirm={() => blocker.proceed?.()}
        open={blocker.state === 'blocked'}
        title="放弃未保存的修改？"
      />
    </>
  )
}

function ModelCard({
  group,
  onMultiplier,
  onOffer,
  onRemove,
}: {
  group: ModelGroup
  onMultiplier: (value: string) => void
  onOffer: (protocol: ChannelProtocol, patch: Partial<OfferDraft>) => void
  onRemove: () => void
}) {
  const removable = !protocols.some((protocol) => group.offers[protocol].id)
  return (
    <article className="sharing-model">
      <header>
        <div className="sharing-model-name">
          <strong>{group.modelName}</strong>
          <small>{group.provider} · {group.modelID}</small>
        </div>
        <TextField
          className="sharing-multiplier"
          inputMode="decimal"
          label="价格倍率"
          min="0"
          onChange={(event) => onMultiplier(event.target.value)}
          required
          step="0.000000001"
          type="number"
          value={group.multiplier}
        />
        {removable && <Button aria-label={`移除 ${group.modelName}`} onClick={onRemove} type="button" variant="quiet">移除</Button>}
      </header>
      <div className="sharing-protocols">
        {protocols.map((protocol) => {
          const draft = group.offers[protocol]
          return (
            <div className={`sharing-protocol ${draft.selected ? 'sharing-protocol-on' : ''}`} key={protocol}>
              <Checkbox
                checked={draft.selected}
                label={protocolLabels[protocol]}
                onChange={(event) => onOffer(protocol, { selected: event.target.checked, selectionTouched: true })}
              />
              <TextField
                aria-label={`${group.modelName} ${protocolLabels[protocol]} 上游模型 ID`}
                disabled={!draft.selected}
                label="上游模型 ID"
                onChange={(event) => onOffer(protocol, { upstreamModelID: event.target.value, upstreamTouched: true })}
                required={draft.selected}
                value={draft.upstreamModelID}
              />
            </div>
          )
        })}
      </div>
    </article>
  )
}
