import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { apiGet, pathSegment, request, withQuery, type QueryParams } from '../api/client'
import type { CallDetail, UsageReport } from '../api/types'

/**
 * 调用列表、详情与用量查询。列表 hook 按接口路径参数化，
 * 用户（/api/calls）、渠道（/api/channels/{id}/calls）与管理员（/api/admin/calls）共用。
 */
export type CallListParams = {
 dimension?: string
 dimension_value?: string
 second_dimension?: string
 second_value?: string
 stream?: boolean
 min_duration_ms?: number
 max_duration_ms?: number
 min_tokens?: number
 max_tokens?: number
 min_cost?: string
 max_cost?: string
 min_attempts?: number
 max_attempts?: number
 status_code?: number
 error_code?: string
 end_reason?: string
 delivery?: string
 usage_status?: string
 billed?: boolean
 attempt_channel_id?: string

  /** 请求 ID 精确匹配 */
  request_id?: string
  model?: string
  outcome?: string
  api_key_id?: string
  tag?: string
  format?: string
  from?: string
  to?: string
  /** 管理员视图可用 */
  account_id?: string
  channel_id?: string
}

export type CursorPage<T> = { items: T[]; next_cursor: string | null }

export const callKeys = {
  all: ['calls'] as const,
  list: (path: string, params: CallListParams) => ['calls', 'list', path, params] as const,
  detail: (path: string, id: string) => ['calls', 'detail', path, id] as const,
  usage: (params: QueryParams) => ['calls', 'usage', params] as const,
}

export const userCallsPath = '/api/calls'
export const userCallsStreamPath = '/api/calls/stream'

/** 游标分页的调用列表；Page 为该接口的响应类型（CallPage / ChannelCallPage / AdminCallPage）。 */
export function useCallPages<Page extends CursorPage<unknown>>(
  path: string,
  params: CallListParams,
  { limit = 50, enabled = true, refetchInterval = false }: { limit?: number; enabled?: boolean; refetchInterval?: number | false } = {},
) {
  return useInfiniteQuery({
    queryKey: [...callKeys.list(path, params), limit],
    queryFn: ({ pageParam, signal }) =>
      request<Page>(withQuery(path, { ...params, limit, cursor: pageParam || undefined }), { signal }),
    initialPageParam: '',
    getNextPageParam: (last: Page) => last.next_cursor ?? undefined,
    enabled,
    refetchInterval,
    placeholderData: keepPreviousData,
    refetchOnWindowFocus: true,
  })
}

/** 调用详情；detailPath 为不含 ID 的路径前缀，默认用户视图。 */
export function useCall(id: string | null, detailPath = userCallsPath) {
  return useQuery({
    queryKey: callKeys.detail(detailPath, id ?? ''),
    queryFn: async ({ signal }) =>
      (await request<{ call: CallDetail }>(`${detailPath}/${pathSegment(id ?? '')}`, { signal })).call,
    enabled: Boolean(id),
    refetchInterval: query => query.state.data?.outcome === 'in_progress' ? 5000 : false,
  })
}

export type UsageParams = {
  group_by: 'day' | 'model' | 'key' | 'tag'
  from?: string
  to?: string
  api_key_id?: string
  model?: string
  tag?: string
}

export function useUsage(params: UsageParams) {
  return useQuery({
    queryKey: callKeys.usage(params),
    queryFn: (): Promise<UsageReport> => apiGet<'getUsage'>('/api/usage', params),
  })
}
