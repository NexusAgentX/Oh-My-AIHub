import { useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import type { MarketOffer } from '../api/types'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'
import { JoinRouteDrawer } from '../gateway/JoinRouteDrawer'
import {
  PricePair,
  formatDate,
  formatRate,
  protocolLabels,
  qualitySummary,
  ratingText,
} from '../gateway/presentation'
import {
  Badge,
  Button,
  ButtonLink,
  Card,
  CountBadge,
  DataTable,
  EmptyState,
  InlineError,
  PageHeader,
  QueryBoundary,
  SuccessMessage,
  type Column,
} from '../ui'
import { MarketRating } from './MarketRating'
import { useMarketChannelQuery, useRateChannelMutation } from './marketQueries'

export function MarketChannelPage() {
  const { channelID = '' } = useParams()
  const query = useMarketChannelQuery(channelID)
  return (
    <QueryBoundary errorFallback="渠道详情加载失败" query={query}>
      {(channel) => <ChannelDetail channel={channel} key={channel.id} />}
    </QueryBoundary>
  )
}

function ChannelDetail({
  channel,
}: {
  channel: NonNullable<ReturnType<typeof useMarketChannelQuery>['data']>
}) {
  const { account } = useAuth()
  const [searchParams, setSearchParams] = useSearchParams()
  const rate = useRateChannelMutation(channel.id)
  const [message, setMessage] = useState<{ text: string; keyID?: string } | null>(null)

  // ?add=<offerID> 打开加入路由抽屉
  const addParam = searchParams.get('add')
  const joining = addParam
    ? (channel.offers.find((offer) => offer.offer_id === addParam) ?? channel.offers[0] ?? null)
    : null
  const joinOpen = Boolean(addParam)

  const setJoining = (offerID: string | null) => {
    const next = new URLSearchParams(searchParams)
    if (offerID) next.set('add', offerID)
    else next.delete('add')
    setSearchParams(next, { replace: true })
  }

  const own = channel.owner_account_id === account?.id
  const published = channel.status === 'published'

  const columns: Column<MarketOffer>[] = [
    {
      key: 'model',
      header: '模型',
      primary: true,
      cell: (offer) => (
        <>
          <strong>{offer.model_name}</strong>
          <small>{offer.model_provider}</small>
        </>
      ),
    },
    { key: 'protocol', header: 'API 格式', cell: (offer) => protocolLabels[offer.protocol] },
    { key: 'multiplier', header: '倍率', numeric: true, hideOnMobile: true, cell: (offer) => `${offer.multiplier}×` },
    {
      key: 'price',
      header: '输入 / 输出',
      cell: (offer) => (
        <PricePair first={offer.input_price} second={offer.output_price} tiers={offer.price_tiers} />
      ),
    },
    {
      key: 'cache',
      header: '缓存写 / 读',
      hideOnMobile: true,
      cell: (offer) => <PricePair first={offer.cache_write_price} second={offer.cache_read_price} />,
    },
    {
      key: 'quality',
      header: '质量',
      cell: (offer) =>
        offer.call_success_rate === null ? (
          <>
            <span className="muted">暂无调用数据</span>
            <small>验证 {formatDate(offer.last_tested_at)}</small>
          </>
        ) : (
          <>
            <strong>{formatRate(offer.call_success_rate)}</strong>
            <small>{qualitySummary(offer)}</small>
          </>
        ),
    },
    {
      key: 'action',
      header: '操作',
      cell: (offer) => (
        <Button onClick={() => setJoining(offer.offer_id)} size="sm" variant="secondary">
          加入路由
        </Button>
      ),
    },
  ]

  return (
    <>
      <PageHeader
        actions={
          <Badge tone={published ? 'success' : 'warning'}>{published ? '已发布' : '已暂停'}</Badge>
        }
        back={
          <Link className="back-link" to="/market">
            ← API 市场
          </Link>
        }
        title={channel.display_name}
      />
      {message && (
        <SuccessMessage>
          {message.text}
          {message.keyID && (
            <>
              {' '}
              <Link to={`/keys/${message.keyID}`}>查看 Key</Link>
            </>
          )}
        </SuccessMessage>
      )}
      <InlineError>{rate.isError ? errorMessage(rate.error, '评分保存失败') : ''}</InlineError>

      <Card className="market-channel-summary">
        <div className="market-owner-info">
          <span aria-hidden="true" className="market-avatar">
            {channel.owner_display_name.slice(0, 1)}
          </span>
          <div>
            <strong>{channel.owner_display_name}</strong>
            <small>共享者</small>
            {own && <Badge tone="info">我的 · 0 手续费</Badge>}
          </div>
        </div>
        <div className="market-rating-info">
          <strong className="num">{ratingText(channel.average_rating, channel.rating_count)}</strong>
          <MarketRating
            disabled={rate.isPending}
            onChange={(score) => {
              setMessage(null)
              rate.mutate(score, { onSuccess: () => setMessage({ text: '评分已保存' }) })
            }}
            value={channel.current_user_rating}
          />
        </div>
      </Card>

      <Card
        actions={<CountBadge>{channel.offers.length}</CountBadge>}
        className="market-channel-offers"
        flush
        title="可用报价"
      >
        <DataTable
          caption="可用报价"
          columns={columns}
          empty={
            <EmptyState
              action={<ButtonLink to="/market">返回市场</ButtonLink>}
              title="渠道当前不可用"
            />
          }
          rowKey={(offer) => offer.offer_id}
          rows={channel.offers}
        />
      </Card>

      <JoinRouteDrawer
        channel={channel}
        offer={joining}
        onClose={() => setJoining(null)}
        onJoined={(key) => {
          setJoining(null)
          setMessage({ text: `已加入 ${key.display_name} 的路由`, keyID: key.id })
        }}
        open={joinOpen}
      />
    </>
  )
}
