import { QueryClient } from '@tanstack/react-query'
import { ApiError } from './client'

/**
 * 查询约定（详见 ADR 0015）：
 * 1. 每个领域在 `<domain>/queries.ts` 中导出 key 工厂与 `useXxx` 查询 hook，页面不直接写 key 或调用 `api.*` 取数。
 * 2. key 形如 `[领域, 资源, ...参数]`，例如 `['wallet', 'entries']`；使用 `as const`。
 * 3. 写操作用 `useMutation`，成功后按领域前缀 `invalidateQueries({ queryKey: ['wallet'] })` 失效，不手动拼装缓存。
 * 4. 错误统一经 `errorMessage` 转为可读中文；页面用 `QueryBoundary` 渲染 加载 / 错误 / 空 / 内容。
 * 5. 会话切换（登录 / 退出 / 换账号）时整体清空缓存，见 App.tsx 的 SessionQueryReset。
 */

/** 把任意错误映射为可展示文案：ApiError 使用后端给出的中文 message，其余使用兜底。 */
export function errorMessage(error: unknown, fallback: string) {
  if (error instanceof ApiError) return error.message
  return fallback
}

/** 4xx 是确定性的业务/权限错误，重试无意义；网络与 5xx 最多重试 2 次。 */
export function shouldRetry(failureCount: number, error: unknown) {
  if (error instanceof ApiError && error.status >= 400 && error.status < 500) {
    return false
  }
  return failureCount < 2
}

export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        retry: shouldRetry,
        refetchOnWindowFocus: false,
      },
      mutations: { retry: false },
    },
  })
}
