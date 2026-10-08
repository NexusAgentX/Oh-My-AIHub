import { useSearchParams } from 'react-router-dom'
import type { C2CMarket, C2COrder } from '../api/types'
import { useAuth } from '../auth/AuthProvider'
import { formatPointAmount } from '../wallet/presentation'
import {
  Badge,
  Button,
  ButtonLink,
  Card,
  DataTable,
  EmptyState,
  Icon,
  Metric,
  MetricGrid,
  PageHeader,
  QueryBoundary,
  type Column,
} from '../ui'
import { useQueryClient } from '@tanstack/react-query'
import { TakeOrderDrawer } from './TakeOrderDrawer'
import {
  c2cPaymentLabels,
  formatC2CDate,
  formatC2CPrice,
} from './presentation'
import { c2cKeys, useC2CMarketQuery } from './queries'

function price(value: number | null) {
  return value === null ? '—' : formatC2CPrice(value)
}

function MarketMetrics({ metrics }: { metrics: C2CMarket['metrics'] }) {
  return (
    <MetricGrid label="C2C 行情">
      <Metric hint="人民币 / 积分" label="指导价" value={price(metrics.guidance_price_fen)} />
      <Metric hint="人民币 / 积分" label="最近成交" value={price(metrics.latest_price_fen)} />
      <Metric hint="最低卖价" label="卖一" tone="accent" value={price(metrics.best_ask_fen)} />
    </MetricGrid>
  )
}

function orderColumns(accountID: string, onTake: (order: C2COrder) => void): Column<C2COrder>[] {
  return [
    {
      key: 'owner',
      header: '用户',
      primary: true,
      cell: (order) => (
        <span className="c2c-owner">
          <strong>{order.owner_display_name}</strong>
          {order.owner_account_id === accountID && <Badge tone="accent">我的</Badge>}
        </span>
      ),
    },
    {
      key: 'price',
      header: '单价',
      numeric: true,
      cell: (order) => formatC2CPrice(order.unit_price_fen),
    },
    {
      key: 'available',
      header: '可成交',
      numeric: true,
      cell: (order) => `${formatPointAmount(order.available)} 积分`,
    },
    {
      key: 'limit',
      header: '单次限额',
      numeric: true,
      cell: (order) => `${formatPointAmount(order.minimum)} – ${formatPointAmount(order.maximum)}`,
    },
    {
      key: 'payment',
      header: '支付方式',
      cell: (order) => order.payment_types.map((type) => c2cPaymentLabels[type]).join('、'),
    },
    {
      key: 'created',
      header: '发布时间',
      hideOnMobile: true,
      cell: (order) => formatC2CDate(order.created_at),
    },
    {
      key: 'action',
      header: '操作',
      cell: (order) =>
        order.owner_account_id === accountID ? (
          <ButtonLink size="sm" to="/c2c/me">管理</ButtonLink>
        ) : (
          <Button disabled={!order.takeable} onClick={() => onTake(order)} size="sm">
            购买
          </Button>
        ),
    },
  ]
}

function OrderBook({ market, accountID, onTake }: { market: C2CMarket; accountID: string; onTake: (order: C2COrder) => void }) {
  return (
    <Card className="c2c-table-card" flush>
      <DataTable
        caption="卖单"
        columns={orderColumns(accountID, onTake)}
        empty={
          <EmptyState
            action={<ButtonLink size="sm" to="/c2c/orders/new">发布卖单</ButtonLink>}
            title="暂无卖单"
          />
        }
        rowKey={(order) => order.id}
        rows={market.sell_orders}
      />
    </Card>
  )
}

export function C2CMarketPage() {
  const { account } = useAuth()
  const query = useC2CMarketQuery()
  const queryClient = useQueryClient()
  const [params, setParams] = useSearchParams()
  const takeID = params.get('take') ?? ''

  const setTake = (orderID: string) => {
    setParams(orderID ? { take: orderID } : {}, { replace: true })
  }

  return (
    <>
      <PageHeader
        actions={
          <>
            <Button
              icon={<Icon name="refresh" />}
              onClick={() => void queryClient.invalidateQueries({ queryKey: c2cKeys.market() })}
              variant="quiet"
            >
              刷新
            </Button>
            <ButtonLink to="/c2c/me">我的挂单与交易</ButtonLink>
            <ButtonLink icon={<Icon name="plus" />} to="/c2c/orders/new" variant="primary">发布卖单</ButtonLink>
          </>
        }
        title="C2C 市场"
      />
      <QueryBoundary errorFallback="C2C 行情加载失败" query={query}>
        {(market) => (
          <>
            <MarketMetrics metrics={market.metrics} />
            <OrderBook accountID={account?.id ?? ''} market={market} onTake={(order) => setTake(order.id)} />
          </>
        )}
      </QueryBoundary>
      <TakeOrderDrawer onClose={() => setTake('')} orderID={takeID} />
    </>
  )
}
