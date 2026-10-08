import { useEffect, useState } from 'react'
import { errorMessage } from '../api/query'
import type { RoutingPreference } from '../api/types'
import { routingModeLabels } from '../calls'
import { useModel, useModels } from '../models/queries'
import { RoutingEditor } from '../models/RoutingEditor'
import { draftFromPreference, sameDraft, toRoutingInput, type RoutingDraft } from '../models/routing'
import { Button, ConfirmDialog, InlineError, QueryBoundary, Segmented } from '../ui'
import { useKeyRouting } from './queries'

function defaultPreference(model: string): RoutingPreference {
  return {
    model_id: model,
    source: 'default',
    mode: 'cheapest',
    order: [],
    excluded: [],
    max_attempts: null,
    ttft_timeout_ms: null,
    updated_at: null,
  }
}

/** 单个模型的 Key 级路由：复用模型详情的编辑器。 */
function KeyModelRouting({
  keyId,
  preference,
  saved,
  onRemoved,
}: {
  keyId: string
  preference: RoutingPreference
  saved: boolean
  onRemoved: () => void
}) {
  const model = useModel(preference.model_id)
  const { set, remove } = useKeyRouting(keyId)
  const [draft, setDraft] = useState<RoutingDraft | null>(null)
  const channels = model.data?.channels
  // 未保存的条目每次渲染都会生成新对象，用序列化值作为依赖避免反复重置草稿
  const preferenceKey = JSON.stringify(preference)
  useEffect(() => {
    if (channels) setDraft(draftFromPreference(JSON.parse(preferenceKey) as RoutingPreference, channels))
  }, [channels, preferenceKey])
  const base = channels ? draftFromPreference(preference, channels) : null
  const dirty = Boolean(draft && base && (!saved || !sameDraft(draft, base)))
  const error = set.error ?? remove.error
  return (
    <section className="key-route">
      <header className="key-route-head">
        <strong className="mono">{preference.model_id}</strong>
        {saved && <span className="muted">{routingModeLabels[preference.mode]}</span>}
        <span className="key-route-actions">
          <Button
            disabled={!dirty}
            loading={set.isPending}
            onClick={() => draft && set.mutate({ model: preference.model_id, preference: toRoutingInput(draft) })}
            size="sm"
            type="button"
          >
            保存
          </Button>
          <Button
            loading={remove.isPending}
            onClick={() => (saved ? remove.mutate(preference.model_id, { onSuccess: onRemoved }) : onRemoved())}
            size="sm"
            type="button"
            variant="quiet"
          >
            移除
          </Button>
        </span>
      </header>
      <InlineError>{error ? errorMessage(error, '保存失败，请重试') : ''}</InlineError>
      <QueryBoundary errorFallback="渠道加载失败" query={model}>
        {() => draft && channels && <RoutingEditor channels={channels} draft={draft} onChange={setDraft} />}
      </QueryBoundary>
    </section>
  )
}

/** Key 的路由：跟随账号，或按模型单独设置。 */
export function KeyRoutingSection({ keyId, routing }: { keyId: string; routing: RoutingPreference[] }) {
  const models = useModels()
  const { remove } = useKeyRouting(keyId)
  const [pending, setPending] = useState<string[]>([])
  const [custom, setCustom] = useState(routing.length > 0)
  const [adding, setAdding] = useState('')
  const [confirmFollow, setConfirmFollow] = useState(false)
  const savedModels = routing.map((item) => item.model_id)
  const entries = [
    ...routing.map((item) => ({ preference: item, saved: true })),
    ...pending.filter((model) => !savedModels.includes(model)).map((model) => ({ preference: defaultPreference(model), saved: false })),
  ]
  const choices = (models.data?.items ?? []).filter((model) => !entries.some((entry) => entry.preference.model_id === model.id))

  const followAccount = async () => {
    for (const model of savedModels) await remove.mutateAsync(model)
    setPending([])
    setCustom(false)
    setConfirmFollow(false)
  }

  return (
    <div className="key-routing">
      <Segmented
        label="路由"
        onChange={(value) => {
          if (value === 'follow' && savedModels.length > 0) setConfirmFollow(true)
          else setCustom(value === 'custom')
        }}
        options={[
          { key: 'follow', label: '跟随账号' },
          { key: 'custom', label: '单独设置' },
        ]}
        value={custom ? 'custom' : 'follow'}
      />
      {custom && (
        <>
          {entries.map((entry) => (
            <KeyModelRouting
              key={entry.preference.model_id}
              keyId={keyId}
              onRemoved={() => setPending((list) => list.filter((model) => model !== entry.preference.model_id))}
              preference={entry.preference}
              saved={entry.saved}
            />
          ))}
          <div className="key-route-add">
            <select
              aria-label="选择模型"
              className="input select-input"
              onChange={(event) => setAdding(event.target.value)}
              value={adding}
            >
              <option value="">选择模型</option>
              {choices.map((model) => (
                <option key={model.id} value={model.id}>
                  {model.id}
                </option>
              ))}
            </select>
            <Button
              disabled={!adding}
              onClick={() => {
                setPending((list) => [...list, adding])
                setAdding('')
              }}
              size="sm"
              type="button"
              variant="secondary"
            >
              添加模型
            </Button>
          </div>
          {models.isError && <p className="muted-copy">{errorMessage(models.error, '模型列表加载失败')}</p>}
        </>
      )}
      <ConfirmDialog
        busy={remove.isPending}
        confirmLabel="跟随账号"
        error={remove.isError ? errorMessage(remove.error, '操作失败，请重试') : ''}
        onClose={() => setConfirmFollow(false)}
        onConfirm={() => void followAccount().catch(() => undefined)}
        open={confirmFollow}
        title={`移除 ${savedModels.length} 个模型的单独路由？`}
      />
    </div>
  )
}
