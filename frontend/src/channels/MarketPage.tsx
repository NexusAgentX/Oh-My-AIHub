import { useEffect, useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { MarketOffer } from '../api/contracts'
import { useAuth } from '../auth/AuthProvider'
import {
  formatDate,
  formatRate,
  isProtocol,
  PricePair,
  protocolLabels,
  ratingText,
} from '../gateway/presentation'
import {
  Badge,
  Button,
  Card,
  CountBadge,
  DataTable,
  EmptyState,
  ErrorState,
  Icon,
  LoadingState,
  PageHeader,
  SearchInput,
  SelectField,
  Toolbar,
  type Column,
} from '../ui'
import {
  useCatalogModelsQuery,
  useMarketOffersQuery,
  type MarketFilters,
  type MarketSort,
} from './marketQueries'
import { errorMessage } from '../api/query'

const sortOptions: Array<{ key: MarketSort; label: string }> = [
  { key: 'input_price', label: '输入价格' },
  { key: 'output_price', label: '输出价格' },
  { key: 'cache_write_price', label: '缓存写价格' },
  { key: 'cache_read_price', label: '缓存读价格' },
  { key: 'rating', label: '用户评分' },
  { key: 'success_rate', label: '成功率' },
  { key: 'ttft', label: '首字响应' },
  { key: 'tps', label: '输出速度' },
]

function isSort(value: string | null): value is MarketSort {
  return sortOptions.some((option) => option.key === value)
}

/** 筛选条件以 URL 查询参数为唯一来源，便于分享与从待处理事项直接定位。 */
export function filtersFromParams(params: URLSearchParams): MarketFilters {
  const protocol = params.get('protocol')
  const sort = params.get('sort')
  return {
    modelID: params.get('model') ?? '',
    protocol: isProtocol(protocol) ? protocol : '',
    owner: params.get('owner') ?? '',
    sort: isSort(sort) ? sort : 'input_price',
  }
}

export function paramsFromFilters(filters: MarketFilters) {
  const params = new URLSearchParams()
  if (filters.modelID) params.set('model', filters.modelID)
  if (filters.protocol) params.set('protocol', filters.protocol)
  if (filters.owner) params.set('owner', filters.owner)
  if (filters.sort !== 'input_price') params.set('sort', filters.sort)
  return params
}

export function hasActiveFilters(filters: MarketFilters) {
  return Boolean(filters.modelID || filters.protocol || filters.owner)
}

export function MarketPage() {
  const { account } = useAuth()
  const [searchParams, setSearchParams] = useSearchParams()
  const filters = filtersFromParams(searchParams)
  const models = useCatalogModelsQuery()
  const query = useMarketOffersQuery(filters)
  const [owner, setOwner] = useState(filters.owner)

  useEffect(() => setOwner(filters.owner), [filters.owner])

  const apply = (patch: Partial<MarketFilters>) =>
    setSearchParams(paramsFromFilters({ ...filters, ...patch }), { replace: true })
  const clear = () => setSearchParams(paramsFromFilters({ ...filters, modelID: '', protocol: '', owner: '' }), { replace: true })

  const offers = [
    ...new Map(
      (query.data?.pages ?? []).flatMap((page) => page.offers).map((offer) => [offer.offer_id, offer]),
    ).values(),
  ]

  const columns: Column<MarketOffer>[] = [
    {
      key: 'channel',
      header: '模型 / 渠道',
      primary: true,
      cell: (offer) => (
        <>
          <strong>{offer.model_name}</strong>
          <small>
            {offer.channel_display_name} · {offer.owner_display_name}
          </small>
          {offer.owner_account_id === account?.id && <Badge tone="info">我的 · 0 手续费</Badge>}
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
            <small>
              {offer.call_count ?? 0} 次 · {offer.ttft_milliseconds ?? '—'} ms ·{' '}
              {offer.tokens_per_second ?? '—'} tok/s
            </small>
          </>
        ),
    },
    {
      key: 'rating',
      header: '评分',
      cell: (offer) => ratingText(offer.average_rating, offer.rating_count),
    },
  ]

  const submitOwner = (event: FormEvent) => {
    event.preventDefault()
    apply({ owner: owner.trim() })
  }

  return (
    <>
      <PageHeader title="API 市场" />
      <Card className="market-filters">
        <Toolbar
          end={
            hasActiveFilters(filters) ? (
              <Button icon={<Icon name="x" />} onClick={clear} size="sm" variant="quiet">
                清除筛选
              </Button>
            ) : undefined
          }
        >
          <div className="market-filter-grid">
            <SelectField
              label="模型"
              onChange={(event) => apply({ modelID: event.target.value })}
              value={filters.modelID}
            >
              <option value="">全部模型</option>
              {(models.data ?? []).map((model) => (
                <option key={model.id} value={model.id}>
                  {model.provider} · {model.name}
                </option>
              ))}
            </SelectField>
            <SelectField
              label="API 格式"
              onChange={(event) =>
                apply({ protocol: isProtocol(event.target.value) ? event.target.value : '' })
              }
              value={filters.protocol}
            >
              <option value="">全部格式</option>
              {Object.entries(protocolLabels).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </SelectField>
            <SelectField
              label="排序"
              onChange={(event) => apply({ sort: event.target.value as MarketSort })}
              value={filters.sort}
            >
              {sortOptions.map((option) => (
                <option key={option.key} value={option.key}>
                  {option.label}
                </option>
              ))}
            </SelectField>
            <form className="market-owner" onSubmit={submitOwner}>
              <SearchInput
                label="共享者"
                onChange={(event) => setOwner(event.target.value)}
                placeholder="共享者"
                value={owner}
              />
            </form>
          </div>
        </Toolbar>
      </Card>

      <Card
        actions={<CountBadge>{offers.length}</CountBadge>}
        className="market-offers"
        flush
        title="可用报价"
      >
        {query.isPending ? (
          <LoadingState />
        ) : query.isError && offers.length === 0 ? (
          <ErrorState message={errorMessage(query.error, 'API 市场加载失败')} onRetry={() => void query.refetch()} />
        ) : (
          <>
            <DataTable
              caption="可用报价"
              columns={columns}
              empty={
                <EmptyState
                  action={
                    hasActiveFilters(filters) ? (
                      <Button onClick={clear} variant="secondary">
                        清除筛选
                      </Button>
                    ) : undefined
                  }
                  title="没有匹配的报价"
                />
              }
              rowHref={(offer) => `/market/channels/${offer.channel_id}`}
              rowKey={(offer) => offer.offer_id}
              rows={offers}
            />
            {query.hasNextPage && (
              <div className="table-pagination">
                <Button
                  loading={query.isFetchingNextPage}
                  onClick={() => void query.fetchNextPage()}
                  variant="secondary"
                >
                  加载更多
                </Button>
              </div>
            )}
          </>
        )}
      </Card>
    </>
  )
}

