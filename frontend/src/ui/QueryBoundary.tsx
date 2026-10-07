import type { ReactNode } from 'react'
import { errorMessage } from '../api/query'
import { ErrorState, LoadingState } from './Feedback'

type QueryLike<T> = {
  data: T | undefined
  isPending: boolean
  isError: boolean
  error: unknown
  refetch: () => unknown
}

/**
 * 把 TanStack Query 结果映射为统一的 加载 / 错误 / 空 / 内容 四态。
 * - 首次加载显示 LoadingState；
 * - 出错且没有可展示的旧数据时显示 ErrorState（带重试）；
 * - isEmpty 返回 true 时显示 empty；
 * - 其余渲染 children(data)。后台刷新失败时保留旧数据，不闪成错误页。
 */
export function QueryBoundary<T>({
  query,
  errorFallback = '加载失败，请稍后重试',
  isEmpty,
  empty,
  children,
}: {
  query: QueryLike<T>
  errorFallback?: string
  isEmpty?: (data: T) => boolean
  empty?: ReactNode
  children: (data: T) => ReactNode
}) {
  if (query.data === undefined) {
    if (query.isPending) return <LoadingState />
    if (query.isError) {
      return (
        <ErrorState
          message={errorMessage(query.error, errorFallback)}
          onRetry={() => void query.refetch()}
        />
      )
    }
    return null
  }
  if (isEmpty?.(query.data)) return <>{empty}</>
  return <>{children(query.data)}</>
}
