import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { errorMessage } from '../api/query'
import type { ModelDetail } from '../api/types'
import { FormatTags } from '../calls'
import { formatPoints } from '../money/format'
import { Badge, Button, Card, Disclosure, InlineError, PageHeader, QueryBoundary, SuccessMessage } from '../ui'
import { currentPrices, tierCondition } from './pricing'
import { useModel, useSetRouting } from './queries'
import { RoutingEditor } from './RoutingEditor'
import { draftFromPreference, sameDraft, toRoutingInput, type RoutingDraft } from './routing'

function PriceLine({ prices }: { prices: { input: string; output: string; cache_read: string; cache_write: string } }) {
  return (
    <span className="num">
      输入 {formatPoints(prices.input, { digits: 2 })} / 输出 {formatPoints(prices.output, { digits: 2 })} · 缓存读{' '}
      {formatPoints(prices.cache_read, { digits: 2 })} / 写 {formatPoints(prices.cache_write, { digits: 2 })}
    </span>
  )
}

function PriceDetails({ detail }: { detail: ModelDetail }) {
  return (
    <Disclosure title="价格详情">
      <p className="muted-copy">积分 / 百万 token</p>
      <dl className="price-rules">
        <div>
          <dt>基准价</dt>
          <dd>
            <PriceLine prices={detail.model.base_prices} />
          </dd>
        </div>
        {detail.price_tiers.map((tier) => (
          <div key={tier.seq}>
            <dt>
              {tier.name}
              {detail.model.current_tier?.seq === tier.seq && <Badge tone="accent">现在</Badge>}
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
  const { model = '' } = useParams()
  const query = useModel(model)
  return (
    <QueryBoundary errorFallback="模型详情加载失败" query={query}>
      {(detail) => {
        const current = currentPrices(detail)
        return (
          <>
            <PageHeader
              back={
                <Link className="back-link" to="/models">
                  ← 模型
                </Link>
              }
              title={detail.model.display_name || detail.model.id}
            />
            <div className="model-summary">
              <Badge tone="accent">
                现在：{current.name} · 输入 {formatPoints(current.prices.input, { digits: 2 })} / 输出{' '}
                {formatPoints(current.prices.output, { digits: 2 })}
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
