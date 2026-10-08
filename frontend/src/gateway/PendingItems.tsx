import { Link } from 'react-router-dom'
import type { PendingItem } from '../api/types'
import {
  Badge,
  Card,
  CountBadge,
  EmptyState,
  ErrorState,
  Icon,
  LoadingState,
} from '../ui'
import { usePendingItemsQuery } from './queries'

/** 待处理事项列表：条目文案、徽标强度与跳转目标均由后端提供，这里只负责渲染。 */
export function PendingList({ items }: { items: PendingItem[] }) {
  return (
    <ul className="pending-list">
      {items.map((item) => (
        <li key={item.id}>
          <Link className="pending-link" to={item.to}>
            <Badge tone={item.tone}>{item.label}</Badge>
            <span className="pending-copy">
              <strong>{item.title}</strong>
              <small>{item.detail}</small>
            </span>
            <Icon name="chevron-right" />
          </Link>
        </li>
      ))}
    </ul>
  )
}

/** 待处理事项：待放行 / 待付款交易、校验失败或暂停的渠道、单渠道路由与需更新渠道。 */
export function PendingItems() {
  const query = usePendingItemsQuery()
  const items = query.data ?? []
  return (
    <Card
      actions={items.length > 0 ? <CountBadge>{items.length}</CountBadge> : undefined}
      className="pending-items"
      flush
      title="待处理事项"
    >
      {query.isPending ? (
        <LoadingState />
      ) : query.isError ? (
        <ErrorState message="待处理事项加载失败" onRetry={() => void query.refetch()} />
      ) : items.length === 0 ? (
        <EmptyState title="没有待处理事项" />
      ) : (
        <PendingList items={items} />
      )}
    </Card>
  )
}
