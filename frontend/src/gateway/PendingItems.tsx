import { Link } from 'react-router-dom'
import {
  Badge,
  Button,
  Card,
  CountBadge,
  EmptyState,
  ErrorState,
  Icon,
  LoadingState,
} from '../ui'
import { usePendingItems } from './queries'

/** 待处理事项：待放行 / 待付款交易、校验失败或暂停的渠道、单渠道路由与需更新渠道、待评分渠道。 */
export function PendingItems() {
  const pending = usePendingItems()
  return (
    <Card
      actions={pending.items.length > 0 ? <CountBadge>{pending.items.length}</CountBadge> : undefined}
      className="pending-items"
      flush
      title="待处理事项"
    >
      {pending.loading && pending.items.length === 0 ? (
        <LoadingState />
      ) : pending.allFailed ? (
        <ErrorState message="待处理事项加载失败" onRetry={pending.refetch} />
      ) : pending.items.length === 0 && pending.failed === 0 ? (
        <EmptyState title="没有待处理事项" />
      ) : (
        <>
          {pending.items.length > 0 && (
            <ul className="pending-list">
              {pending.items.map((item) => (
                <li key={item.id}>
                  <Link className="pending-link" to={item.to}>
                    <Badge tone={item.tone === 'danger' ? 'danger' : item.tone === 'warning' ? 'warning' : 'info'}>
                      {item.label}
                    </Badge>
                    <span className="pending-copy">
                      <strong>{item.title}</strong>
                      <small>{item.detail}</small>
                    </span>
                    <Icon name="chevron-right" />
                  </Link>
                </li>
              ))}
            </ul>
          )}
          {pending.failed > 0 && (
            <div className="pending-partial" role="status">
              <span>部分事项未能加载</span>
              <Button onClick={pending.refetch} size="sm" variant="quiet">
                重试
              </Button>
            </div>
          )}
        </>
      )}
    </Card>
  )
}
