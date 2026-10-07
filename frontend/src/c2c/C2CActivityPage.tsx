import { useState } from 'react'
import { useAuth } from '../auth/AuthProvider'
import { errorMessage } from '../api/query'
import type { C2COrder, C2CTrade } from '../api/contracts'
import { formatPointAmount } from '../wallet/presentation'
import {
  Badge,
  ButtonLink,
  Button,
  Card,
  DataTable,
  EmptyState,
  PageHeader,
  QueryBoundary,
  Tabs,
  type Column,
} from '../ui'
import { C2CState } from './C2CState'
import { ConfirmDialog } from './ConfirmDialog'
import {
  c2cOrderStatusLabels,
  c2cSideLabels,
  c2cStatusTone,
  c2cTradeActionHint,
  c2cTradeRole,
  c2cTradeStatusLabels,
  formatC2CDate,
  formatC2CFiat,
  formatC2CPrice,
  isC2COrderCancellable,
} from './presentation'
import { useC2CActivityQuery, useCancelC2COrder } from './queries'

// 管理员争议页仍从此文件引用 C2CState。
export { C2CState }

type ActivityTab = 'orders' | 'trades'

function OrdersTable({ orders, onCancel }: { orders: C2COrder[]; onCancel: (order: C2COrder) => void }) {
  const columns: Column<C2COrder>[] = [
    {
      key: 'side',
      header: '方向',
      primary: true,
      cell: (order) => (
        <span className="c2c-owner">
          <strong>{c2cSideLabels[order.side]}</strong>
          <C2CState label={c2cOrderStatusLabels[order.status]} tone={c2cStatusTone(order.status)} />
        </span>
      ),
    },
    { key: 'price', header: '单价', numeric: true, cell: (order) => formatC2CPrice(order.unit_price_fen) },
    {
      key: 'available',
      header: '可成交 / 总量',
      numeric: true,
      cell: (order) => `${formatPointAmount(order.available)} / ${formatPointAmount(order.total)}`,
    },
    {
      key: 'progress',
      header: '处理中 / 已成交',
      numeric: true,
      cell: (order) => `${formatPointAmount(order.allocated)} / ${formatPointAmount(order.settled)}`,
    },
    { key: 'updated', header: '更新', hideOnMobile: true, cell: (order) => formatC2CDate(order.updated_at) },
    {
      key: 'action',
      header: '操作',
      cell: (order) =>
        isC2COrderCancellable(order) ? (
          <Button onClick={() => onCancel(order)} size="sm" variant="danger">取消挂单</Button>
        ) : null,
    },
  ]
  return (
    <DataTable
      caption="我的挂单"
      columns={columns}
      empty={
        <EmptyState
          action={<ButtonLink size="sm" to="/c2c/orders/new" variant="primary">发布挂单</ButtonLink>}
          title="暂无挂单"
        />
      }
      rowKey={(order) => order.id}
      rows={orders}
    />
  )
}

function TradesTable({ trades, accountID }: { trades: C2CTrade[]; accountID: string }) {
  const columns: Column<C2CTrade>[] = [
    {
      key: 'role',
      header: '交易',
      primary: true,
      cell: (trade) => (
        <span className="c2c-owner">
          <strong>{c2cTradeRole(trade, accountID) === 'buyer' ? '购买' : '出售'}</strong>
          {c2cTradeActionHint(trade, accountID) ? (
            <Badge tone="accent">{c2cTradeActionHint(trade, accountID)}</Badge>
          ) : (
            <C2CState label={c2cTradeStatusLabels[trade.status]} tone={c2cStatusTone(trade.status)} />
          )}
        </span>
      ),
    },
    {
      key: 'counterparty',
      header: '对方',
      cell: (trade) =>
        c2cTradeRole(trade, accountID) === 'buyer' ? trade.seller_display_name : trade.buyer_display_name,
    },
    { key: 'quantity', header: '数量', numeric: true, cell: (trade) => `${formatPointAmount(trade.quantity)} 积分` },
    { key: 'fiat', header: '人民币', numeric: true, cell: (trade) => formatC2CFiat(trade.fiat_amount_fen) },
    { key: 'updated', header: '更新', hideOnMobile: true, cell: (trade) => formatC2CDate(trade.updated_at) },
  ]
  return (
    <DataTable
      caption="我的交易"
      columns={columns}
      empty={<EmptyState title="暂无交易" />}
      rowHref={(trade) => `/c2c/trades/${trade.id}`}
      rowKey={(trade) => trade.id}
      rows={trades}
    />
  )
}

function ActivityTabs({ orders, trades, accountID }: { orders: C2COrder[]; trades: C2CTrade[]; accountID: string }) {
  const cancel = useCancelC2COrder()
  const [tab, setTab] = useState<ActivityTab>(() =>
    trades.some((trade) => c2cTradeActionHint(trade, accountID)) || orders.length === 0 ? 'trades' : 'orders',
  )
  const [target, setTarget] = useState<C2COrder | null>(null)
  const close = () => {
    cancel.reset()
    setTarget(null)
  }
  return (
    <>
      <Tabs
        items={[
          { key: 'orders', label: '我的挂单', count: orders.length },
          { key: 'trades', label: '我的交易', count: trades.length },
        ]}
        label="我的 C2C"
        onChange={setTab}
        value={tab}
      >
        <Card className="c2c-table-card" flush>
          {tab === 'orders' ? (
            <OrdersTable onCancel={setTarget} orders={orders} />
          ) : (
            <TradesTable accountID={accountID} trades={trades} />
          )}
        </Card>
      </Tabs>
      <ConfirmDialog
        busy={cancel.isPending}
        confirmLabel="取消挂单"
        danger
        error={cancel.isError ? errorMessage(cancel.error, '挂单取消失败') : ''}
        onClose={close}
        onConfirm={() => target && cancel.mutate(target.id, { onSuccess: close })}
        open={target !== null}
        title={`取消${target ? c2cSideLabels[target.side] : '挂单'}`}
      >
        未成交的数量将不再展示{target?.side === 'sell' ? '，冻结的积分会解冻' : ''}；进行中的交易不受影响。
      </ConfirmDialog>
    </>
  )
}

export function C2CActivityPage() {
  const { account } = useAuth()
  const query = useC2CActivityQuery()
  return (
    <>
      <PageHeader
        actions={<ButtonLink to="/c2c/orders/new" variant="primary">发布挂单</ButtonLink>}
        back={<ButtonLink size="sm" to="/c2c" variant="quiet">← C2C 市场</ButtonLink>}
        title="我的挂单与交易"
      />
      <QueryBoundary errorFallback="C2C 订单加载失败" query={query}>
        {({ orders, trades }) => (
          <ActivityTabs accountID={account?.id ?? ''} orders={orders} trades={trades} />
        )}
      </QueryBoundary>
    </>
  )
}
