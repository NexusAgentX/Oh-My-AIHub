import { useState, type FormEvent } from 'react'
import { ApiError } from '../api/client'
import type { APIKey, MarketChannel, MarketOffer } from '../api/types'
import { errorMessage } from '../api/query'
import {
  Badge,
  Button,
  ButtonLink,
  Drawer,
  EmptyState,
  InlineError,
  QueryBoundary,
  SelectField,
} from '../ui'
import {
  PricePair,
  formatRate,
  protocolLabels,
} from './presentation'
import { useAPIKeysQuery, useAddPoolMemberMutation } from './queries'

/**
 * 加入路由抽屉：把市场报价加入所选 Key 中同「模型 + 协议」的路由，并选择备用顺序。
 * 不再整页跳转；成功后由调用方提示并可前往 Key。
 */
export function JoinRouteDrawer({
  open,
  channel,
  offer,
  onClose,
  onJoined,
}: {
  open: boolean
  channel: MarketChannel
  offer: MarketOffer | null
  onClose: () => void
  onJoined: (key: APIKey) => void
}) {
  const keys = useAPIKeysQuery()
  return (
    <Drawer
      description={`${channel.display_name} · ${channel.owner_display_name}`}
      onClose={onClose}
      open={open}
      title="加入路由"
    >
      <QueryBoundary errorFallback="API Key 加载失败" query={keys}>
        {(all) => (
          <JoinRouteForm
            channel={channel}
            keys={all.filter((key) => key.status !== 'deleted')}
            offer={offer}
            onCancel={onClose}
            onJoined={onJoined}
            onReload={() => keys.refetch()}
          />
        )}
      </QueryBoundary>
    </Drawer>
  )
}

function JoinRouteForm({
  channel,
  keys,
  offer,
  onCancel,
  onJoined,
  onReload,
}: {
  channel: MarketChannel
  keys: APIKey[]
  offer: MarketOffer | null
  onCancel: () => void
  onJoined: (key: APIKey) => void
  onReload: () => Promise<unknown>
}) {
  const add = useAddPoolMemberMutation()
  const [keyID, setKeyID] = useState('')
  const [position, setPosition] = useState<number | null>(null)
  const [error, setError] = useState('')
  const [stale, setStale] = useState(false)

  const selected = keys.find((key) => key.id === keyID) ?? keys[0]
  const route = selected?.pools.find(
    (pool) => offer && pool.model_id === offer.model_id && pool.protocol === offer.protocol,
  )
  const included = Boolean(route?.members.some((member) => member.offer_id === offer?.offer_id))
  const unavailable = channel.status !== 'published' || !offer
  const last = (route?.members.length ?? 0) + 1
  const priority = Math.min(position ?? last, last)

  const previewRows = (route?.members ?? []).map((member) => ({
    id: member.offer_id,
    name: member.channel_name,
    isNew: false,
  }))
  if (!included) {
    previewRows.splice(priority - 1, 0, { id: 'new', name: channel.display_name, isNew: true })
  }

  if (keys.length === 0) {
    return (
      <EmptyState
        action={<ButtonLink to="/keys?new=1" variant="primary">创建 API Key</ButtonLink>}
        title="还没有可用的 API Key"
      />
    )
  }

  const reload = async () => {
    await onReload()
    setStale(false)
    setError('')
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!offer || !selected || unavailable || included) return
    setError('')
    try {
      const key = await add.mutateAsync({
        keyID: selected.id,
        version: selected.version,
        modelID: offer.model_id,
        protocol: offer.protocol,
        offerID: offer.offer_id,
        priority,
      })
      onJoined(key)
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'conflict') {
        setStale(true)
        setError('Key 已被修改，请重新加载后再试')
      } else {
        setError(errorMessage(caught, '加入路由失败'))
      }
    }
  }

  return (
    <form className="join-route" onSubmit={(event) => void submit(event)}>
      <div className="key-editor-body">
        <InlineError>{error}</InlineError>
        {offer && (
          <dl className="detail-list join-route-offer">
            <div>
              <dt>模型 · API 格式</dt>
              <dd>
                {offer.model_name} · {protocolLabels[offer.protocol]}
              </dd>
            </div>
            <div>
              <dt>输入 / 输出</dt>
              <dd>
                <PricePair first={offer.input_price} second={offer.output_price} tiers={offer.price_tiers} />
              </dd>
            </div>
            <div>
              <dt>成功率</dt>
              <dd>{formatRate(offer.call_success_rate)}</dd>
            </div>
          </dl>
        )}
        <SelectField
          disabled={unavailable}
          label="API Key"
          onChange={(event) => {
            setKeyID(event.target.value)
            setPosition(null)
            setStale(false)
            setError('')
          }}
          value={selected?.id ?? ''}
        >
          {keys.map((key) => (
            <option key={key.id} value={key.id}>
              {key.display_name}
            </option>
          ))}
        </SelectField>
        <SelectField
          disabled={unavailable || included || !selected}
          label="备用顺序"
          onChange={(event) => setPosition(Number(event.target.value))}
          value={priority}
        >
          {Array.from({ length: last }, (_, index) => index + 1).map((value) => (
            <option key={value} value={value}>
              {value === 1 ? '1 · 首选' : `${value} · 备用`}
            </option>
          ))}
        </SelectField>
        {route && (
          <ol className="route-members route-members-preview">
            {previewRows.map((row, index) => (
              <li className={row.isNew ? 'route-members-new' : undefined} key={row.id}>
                <span className="route-rank num">{index + 1}</span>
                <span className="route-member-copy">
                  <strong>{row.name}</strong>
                </span>
                {row.isNew && <Badge tone="accent">新增</Badge>}
              </li>
            ))}
          </ol>
        )}
        {included && <InlineError>这个报价已在所选 Key 的路由中</InlineError>}
        {unavailable && <InlineError>渠道当前不可加入路由</InlineError>}
      </div>
      <div className="drawer-actions">
        <Button onClick={onCancel} type="button" variant="secondary">
          取消
        </Button>
        {stale ? (
          <Button onClick={() => void reload()} type="button">
            重新加载 Key
          </Button>
        ) : (
          <Button
            disabled={unavailable || included || !selected || !offer}
            loading={add.isPending}
            type="submit"
          >
            加入
          </Button>
        )}
      </div>
    </form>
  )
}
