import { useEffect, useState } from 'react'
import { useLocation, useParams } from 'react-router-dom'
import { errorMessage } from '../api/query'
import type { ModelDetail } from '../api/types'
import { FormatTags } from '../calls'
import { formatPoints } from '../money/format'
import { Badge, Button, Card, Disclosure, InlineError, PageHeader, QueryBoundary, SuccessMessage } from '../ui'
import { currentPrices, tierCondition } from './pricing'
import { tokenPriceLabels } from './tokenPricing'
import { useModel, useSetRouting } from './queries'
import { RoutingEditor } from './RoutingEditor'
import { draftFromPreference, sameDraft, toRoutingInput, type RoutingDraft } from './routing'

function PriceLine({
  prices,
}: {
  prices: {
    input: string
    output: string
    cache_read: string
    cache_write: string
    token_prices?: Record<string, string>
  }
}) {
  return (
    <span className="num">
      输入 {formatPoints(prices.input, { digits: 9 })} / 输出 {formatPoints(prices.output, { digits: 9 })} · 缓存读{' '}
      {formatPoints(prices.cache_read, { digits: 9 })} / 写 {formatPoints(prices.cache_write, { digits: 9 })}
      {Object.entries(prices.token_prices ?? {}).map(([key, value]) => (
        <small key={key}>
          {' '}
          · {tokenPriceLabels[key]} {value}
        </small>
      ))}
    </span>
  )
}

function PriceDetails({ detail }: { detail: ModelDetail }) {
  return (
    <Disclosure title="价格详情">
      <p className="muted-copy">
        {detail.model.custom && '渠道自定义模型：各渠道自定价格、按渠道价格直接计费，这里是按「(3 × 输入 + 输出) ÷ 4」最便宜的渠道价格；便宜优先按同一口径排序。'}
        积分 / 百万 token。细分价未设置时继承本组通用价；缺少可靠明细时按通用价结算。工具、容器、存储附加费未覆盖。
        {detail.model.usd_cny_rate && ` 美元定价模型，按 1 美元 = ${detail.model.usd_cny_rate} 积分换算。`}
      </p>
      <dl className="price-rules">
        <div>
          <dt>{detail.model.custom ? '最低渠道价' : '基准价'}</dt>
          <dd>
            <PriceLine prices={detail.model.base_prices} />
          </dd>
        </div>
        {detail.price_tiers.map((tier) => (
          <div key={tier.seq}>
            <dt>
              {tier.name}
              {detail.model.current_tier?.seq === tier.seq && <Badge tone="accent">时间参考</Badge>}
            </dt>
            <dd>
              <span className="muted">{tierCondition(tier)}</span>
              <PriceLine prices={tier.prices} />
            </dd>
          </div>
        ))}
      </dl>
    </Disclosure>
  )
}

function RoutingCard({ detail }: { detail: ModelDetail }) {
  const saved = draftFromPreference(detail.routing, detail.channels)
  const [draft, setDraft] = useState<RoutingDraft>(saved)
  const [message, setMessage] = useState('')
  const mutation = useSetRouting(detail.model.id)
  const dirty = !sameDraft(draft, saved)
  useEffect(() => {
    setDraft(draftFromPreference(detail.routing, detail.channels))
  }, [detail.routing, detail.channels])
  return (
    <Card
      actions={
        <Button
          disabled={!dirty}
          loading={mutation.isPending}
          onClick={() => {
            setMessage('')
            mutation.mutate(toRoutingInput(draft), { onSuccess: () => setMessage('已保存') })
          }}
          size="sm"
          type="button"
        >
          保存
        </Button>
      }
      title="调用方式"
    >
      <SuccessMessage>{dirty ? '' : message}</SuccessMessage>
      <InlineError>{mutation.isError ? errorMessage(mutation.error, '保存失败，请重试') : ''}</InlineError>
      <RoutingEditor
        channels={detail.channels}
        draft={draft}
        onChange={setDraft}
        scopeHint="对你所有 Key 生效，单把 Key 可以另设"
      />
    </Card>
  )
}

export function ModelDetailPage() {
  const location = useLocation()
  const { model = '' } = useParams()
  const query = useModel(model)
  return (
    <QueryBoundary errorFallback="模型详情加载失败" query={query}>
      {(detail) => {
        const current = currentPrices(detail)
        return (
          <>
            <PageHeader
              back={{ to: location.state?.catalogReturn || '/models', state: { catalogKey: location.state?.catalogKey }, label: '模型' }}
              title={detail.model.display_name || detail.model.id}
            />
            <div className="model-summary">
              {detail.model.custom && <Badge tone="info">自定义模型</Badge>}
              <Badge tone="accent">
                参考价（实际按调用条件）：{detail.model.custom ? '最低渠道价' : current.name} · 输入 {formatPoints(current.prices.input, { digits: 9 })} /
                输出 {formatPoints(current.prices.output, { digits: 9 })}
              </Badge>
              <span className="model-summary-formats">
                <span className="muted">可用格式</span>
                <FormatTags formats={detail.model.formats} />
              </span>
            </div>
            <PriceDetails detail={detail} />
            <RoutingCard detail={detail} />
          </>
        )
      }}
    </QueryBoundary>
  )
}
