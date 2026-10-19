import { Button } from './Button'
import { InlineError } from './Feedback'

export function Pager({ hasMore, loading, error, limited, onMore }: {
  hasMore: boolean; loading: boolean; error?: string; limited?: boolean; onMore: () => void
}) {
  if (!hasMore && !error) return null
  return <div className="table-pagination">
    <InlineError>{error}</InlineError>
    {hasMore && (limited ? <p className="muted-copy">已显示 10 页，请缩小筛选范围。</p> :
      <Button variant="secondary" size="sm" loading={loading} onClick={onMore}>{error ? '重试加载' : '加载更多'}</Button>)}
  </div>
}
