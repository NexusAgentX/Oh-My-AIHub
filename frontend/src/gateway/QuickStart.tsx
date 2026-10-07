import { useState } from 'react'
import type { APIKey, ChannelProtocol } from '../api/contracts'
import { errorMessage } from '../api/query'
import {
  useCatalogModelsQuery,
  useRouteOffersQuery,
} from '../channels/marketQueries'
import {
  Badge,
  Button,
  ButtonLink,
  Card,
  Checkbox,
  EmptyState,
  InlineError,
  LoadingState,
  Notice,
  QueryBoundary,
  SelectField,
  TextField,
} from '../ui'
import { CallExamples, SecretValue } from './CallExamples'
import {
  PricePair,
  formatRate,
  protocolLabels,
  protocols,
} from './presentation'
import { useCreateKeyMutation } from './queries'

type Created = {
  key: APIKey
  secret: string
  modelID: string
  protocol: ChannelProtocol
}

/**
 * 快速开始：选择模型与原生协议 → 选择渠道（默认价格最低的一个）→
 * 用现有接口创建 Key 与路由 → 展示带真实 Base URL 与 Key 的调用示例。
 */
export function QuickStart() {
  const models = useCatalogModelsQuery()
  const [created, setCreated] = useState<Created | null>(null)

  return (
    <Card className="quick-start" title="快速开始">
      <QueryBoundary
        empty={<EmptyState title="暂无可用模型" description="管理员尚未配置模型目录。" />}
        errorFallback="模型目录加载失败"
        isEmpty={(list) => list.length === 0}
        query={models}
      >
        {(list) =>
          created ? (
            <QuickStartResult created={created} onReset={() => setCreated(null)} />
          ) : (
            <QuickStartForm
              models={list}
              onCreated={setCreated}
            />
          )
        }
      </QueryBoundary>
    </Card>
  )
}

function QuickStartForm({
  models,
  onCreated,
}: {
  models: Array<{ id: string; name: string; provider: string }>
  onCreated: (created: Created) => void
}) {
  const create = useCreateKeyMutation()
  const [modelChoice, setModelChoice] = useState('')
  const [protocol, setProtocol] = useState<ChannelProtocol>('openai_chat_completions')
  const [picked, setPicked] = useState<string[] | null>(null)
  const [name, setName] = useState('')
  const [error, setError] = useState('')

  const modelID = models.some((model) => model.id === modelChoice)
    ? modelChoice
    : (models[0]?.id ?? '')
  const model = models.find((item) => item.id === modelID)
  const offersQuery = useRouteOffersQuery(modelID, protocol)
  const offers = offersQuery.data ?? []
  // 未手动选择时默认选中价格最低的渠道
  const selected = picked ?? (offers[0] ? [offers[0].offer_id] : [])
  const keyName = name.trim() || `快速开始 · ${model?.name ?? modelID}`

  const toggle = (offerID: string) => {
    setPicked(
      selected.includes(offerID)
        ? selected.filter((id) => id !== offerID)
        : [...selected, offerID],
    )
  }

  const submit = async () => {
    setError('')
    // 备用顺序：按列表（价格由低到高）顺序，与勾选先后无关
    const offerIDs = offers.map((offer) => offer.offer_id).filter((id) => selected.includes(id))
    try {
      const result = await create.mutateAsync({
        displayName: keyName,
        pools: [{ model_id: modelID, protocol, offer_ids: offerIDs }],
      })
      onCreated({ key: result.key, secret: result.secret, modelID, protocol })
    } catch (caught) {
      setError(errorMessage(caught, 'Key 创建失败'))
    }
  }

  return (
    <div className="quick-start-form">
      <div className="quick-start-row">
        <SelectField
          label="模型"
          onChange={(event) => {
            setModelChoice(event.target.value)
            setPicked(null)
          }}
          value={modelID}
        >
          {models.map((item) => (
            <option key={item.id} value={item.id}>
              {item.provider} · {item.name}
            </option>
          ))}
        </SelectField>
        <SelectField
          label="API 格式"
          onChange={(event) => {
            setProtocol(event.target.value as ChannelProtocol)
            setPicked(null)
          }}
          value={protocol}
        >
          {protocols.map((value) => (
            <option key={value} value={value}>
              {protocolLabels[value]}
            </option>
          ))}
        </SelectField>
      </div>

      <div className="quick-start-offers">
        <p className="field-label">渠道 · 备用顺序按价格由低到高</p>
        {offersQuery.isPending ? (
          <LoadingState />
        ) : offersQuery.isError ? (
          <InlineError>{errorMessage(offersQuery.error, '市场报价加载失败')}</InlineError>
        ) : offers.length === 0 ? (
          <EmptyState
            action={<ButtonLink to="/market">浏览 API 市场</ButtonLink>}
            title="没有可用渠道"
            description="市场中暂无该模型与 API 格式的报价。"
          />
        ) : (
          <ul className="offer-picker">
            {offers.map((offer) => {
              const rank = selected.indexOf(offer.offer_id)
              const order = offers
                .map((item) => item.offer_id)
                .filter((id) => selected.includes(id))
                .indexOf(offer.offer_id)
              return (
                <li key={offer.offer_id}>
                  <Checkbox
                    checked={rank >= 0}
                    label={
                      <span className="offer-picker-copy">
                        <strong>{offer.channel_display_name}</strong>
                        <small>
                          {offer.owner_display_name} ·{' '}
                          {offer.call_success_rate === null
                            ? '暂无调用数据'
                            : formatRate(offer.call_success_rate)}
                        </small>
                      </span>
                    }
                    onChange={() => toggle(offer.offer_id)}
                  />
                  <PricePair first={offer.input_price} second={offer.output_price} tiers={offer.price_tiers} />
                  {order >= 0 && <Badge tone="accent">{order === 0 ? '首选' : `备用 ${order}`}</Badge>}
                </li>
              )
            })}
          </ul>
        )}
      </div>

      <TextField
        label="Key 名称"
        maxLength={80}
        onChange={(event) => setName(event.target.value)}
        placeholder={`快速开始 · ${model?.name ?? ''}`}
        value={name}
      />
      <InlineError>{error}</InlineError>
      <div className="quick-start-actions">
        <Button
          disabled={!modelID || selected.length === 0}
          loading={create.isPending}
          onClick={() => void submit()}
          type="button"
        >
          创建 Key 并生成示例
        </Button>
      </div>
    </div>
  )
}

function QuickStartResult({
  created,
  onReset,
}: {
  created: Created
  onReset: () => void
}) {
  return (
    <div className="quick-start-result">
      <Notice tone="warning">完整 Key 只显示这一次，离开页面后无法再次查看。</Notice>
      <SecretValue secret={created.secret} />
      <CallExamples apiKey={created.secret} modelID={created.modelID} protocol={created.protocol} />
      <div className="quick-start-actions">
        <ButtonLink to={`/keys/${created.key.id}`} variant="secondary">
          查看 Key
        </ButtonLink>
        <Button onClick={onReset} type="button" variant="quiet">
          我已保存，再创建一个
        </Button>
      </div>
    </div>
  )
}
