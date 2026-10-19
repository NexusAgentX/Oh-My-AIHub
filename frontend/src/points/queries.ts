import { statusMeta } from '../ui/status'
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiGet, apiSend, pathSegment, request, withQuery, type QueryParams } from '../api/client'
import type { RequestBody } from '../api/types'
import type { C2CMyOrder, C2COrder, C2CTrade, PointsEntryPage, ResponseBody, TradeStatus } from '../api/types'
import { usePageVisible } from '../api/visibility'

export const pointsKeys = {
  all: ['points'] as const,
  summary: ['points', 'summary'] as const,
  period: (from?: string, to?: string) => ['points', 'period', from, to] as const,
  entries: (params: QueryParams) => ['points', 'entries', params] as const,
}

export const c2cKeys = {
  all: ['c2c'] as const,
  orders: ['c2c', 'orders'] as const,
  myOrders: ['c2c', 'my-orders'] as const,
  myTrades: (params: QueryParams) => ['c2c', 'my-trades', params] as const,
  trade: (id: string) => ['c2c', 'trade', id] as const,
}

/** 当前账户的积分概况（余额、信用额度、可用额度、30 天走势与本月对账）。 */
export function usePoints() {
  const visible = usePageVisible()
  return useQuery({ queryKey: pointsKeys.summary, queryFn: () => apiGet<'getPoints'>('/api/points'), refetchOnWindowFocus: true, refetchInterval: visible ? 60_000 : false })
}

/** 指定期间（如账单所选月份）的对账；与 usePoints 同一接口，按期间单独缓存。 */
export function usePointsPeriod(from?: string, to?: string) {
  return useQuery({
    queryKey: pointsKeys.period(from, to),
    queryFn: () => apiGet<'getPoints'>('/api/points', { from, to }),
    select: (data) => data.period,
  })
}

export type EntryParams = {
  transaction_id?: string
  /** 汇总方式；省略则不返回 summary */
  group?: 'day' | 'key'
  type?: string
  api_key_id?: string
  from?: string
  to?: string
}

export function usePointsEntries(params: EntryParams) {
  return useInfiniteQuery({
    queryKey: pointsKeys.entries(params),
    queryFn: ({ pageParam }) =>
      request<PointsEntryPage>(withQuery('/api/points/entries', { ...params, limit: 50, cursor: pageParam || undefined })),
    initialPageParam: '',
    getNextPageParam: (last: PointsEntryPage) => last.next_cursor ?? undefined,
    placeholderData: keepPreviousData,
  })
}

/** 账单 CSV 导出：先取回再下载，失败时抛出可展示的错误。 */
export async function downloadEntriesCsv(params: EntryParams) {
  const response = await fetch(withQuery('/api/points/entries', { ...params, format: 'csv' }), {
    credentials: 'same-origin',
  })
  if (!response.ok) {
    let message = '导出失败，请稍后重试'
    try {
      const payload = (await response.json()) as { message?: string }
      if (payload.message) message = payload.message
    } catch {
      // 保留兜底文案
    }
    throw new Error(message)
  }
  const blob = await response.blob()
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `points-${new Date().toISOString().slice(0, 10)}.csv`
  link.click()
  URL.revokeObjectURL(url)
}

function useC2CPages<T>(key: readonly unknown[], path: string, params: QueryParams = {}) {
  const visible = usePageVisible()
  const query = useInfiniteQuery({
    queryKey: [...key, params],
    queryFn: ({ pageParam, signal }) => request<{ items: T[]; next_cursor: string | null }>(withQuery(path, { ...params, cursor: pageParam || undefined, limit: 50 }), { signal }),
    initialPageParam: '', getNextPageParam: last => last.next_cursor || undefined,
    placeholderData: keepPreviousData, refetchOnWindowFocus: true,
    refetchInterval: visible && (params.pending || params.active) ? 15_000 : false,
  })
  return { ...query, pageCount: query.data?.pages.length ?? 0, data: query.data ? { items: query.data.pages.flatMap(page => page.items), next_cursor: query.data.pages.at(-1)?.next_cursor ?? null } : undefined }
}
export function useC2COrders() { return useC2CPages<C2COrder>(c2cKeys.orders, '/api/c2c/orders') }

export function useMyC2COrders() {
  return useC2CPages<C2CMyOrder>(c2cKeys.myOrders, '/api/c2c/my/orders')
}

export function useMyC2CTrades(params: { status?: TradeStatus; role?: 'buyer' | 'seller'; pending?: boolean; active?: boolean }) {
  return useC2CPages<C2CTrade>(c2cKeys.myTrades(params), '/api/c2c/my/trades', params)
}

export function useC2COrder(id: string) {
  return useQuery({
    queryKey: ['c2c', 'order', id], enabled: Boolean(id),
    queryFn: async ({ signal }) => (await request<ResponseBody<'getC2CMarketOrder'>>(`/api/c2c/orders/${pathSegment(id)}`, { signal })).order,
    refetchOnWindowFocus: true,
  })
}

export function useC2CTrade(id: string | null) {
  const visible = usePageVisible()
  return useQuery({
    queryKey: c2cKeys.trade(id ?? ''),
    queryFn: async () => (await apiGet<'getC2CTrade'>(`/api/c2c/trades/${pathSegment(id ?? '')}`)).trade,
    enabled: Boolean(id),
    refetchInterval: query => visible && query.state.data && !statusMeta('trade', query.state.data.status).terminal ? 15_000 : false,
    refetchOnWindowFocus: true,
  })
}

function useC2CInvalidate() {
  const client = useQueryClient()
  return () => Promise.all([
    client.invalidateQueries({ queryKey: c2cKeys.all }),
    client.invalidateQueries({ queryKey: pointsKeys.all }),
    client.invalidateQueries({ queryKey: ['home'] }),
  ])
}

export function newIdempotencyKey() {
  return typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(16).slice(2)}`
}

export function useCreateOrder() {
  const invalidate = useC2CInvalidate()
  return useMutation({
    mutationFn: (input: { body: RequestBody<'createC2COrder'>; idempotencyKey: string }) =>
      apiSend<'createC2COrder'>('POST', '/api/c2c/orders', input.body, { idempotencyKey: input.idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useCloseOrder() {
  const invalidate = useC2CInvalidate()
  return useMutation({
    mutationFn: (id: string) => apiSend<'closeC2COrder'>('POST', `/api/c2c/orders/${pathSegment(id)}/close`),
    onSuccess: invalidate,
  })
}

export function useCreateTrade() {
  const invalidate = useC2CInvalidate()
  return useMutation({
    mutationFn: async (input: { orderId: string; amount: string; idempotencyKey: string }) =>
      (
        await apiSend<'createC2CTrade'>(
          'POST',
          `/api/c2c/orders/${pathSegment(input.orderId)}/trades`,
          { amount: input.amount },
          { idempotencyKey: input.idempotencyKey },
        )
      ).trade,
    onSuccess: invalidate,
  })
}

type TradeAction = 'paid' | 'release' | 'cancel' | 'dispute'

/** 交易操作：已付款（可附说明）、放行、取消、申诉（陈述）。 */
export function useTradeAction() {
  const invalidate = useC2CInvalidate()
  return useMutation({
    mutationFn: async (input: { id: string; action: TradeAction; text?: string }) => {
      const path = `/api/c2c/trades/${pathSegment(input.id)}/${input.action}`
      if (input.action === 'paid') return (await apiSend<'markC2CTradePaid'>('POST', path, input.text ? { note: input.text } : {})).trade
      if (input.action === 'dispute') return (await apiSend<'disputeC2CTrade'>('POST', path, { statement: input.text ?? '' })).trade
      if (input.action === 'release') return (await apiSend<'releaseC2CTrade'>('POST', path)).trade
      return (await apiSend<'cancelC2CTrade'>('POST', path)).trade
    },
    onSuccess: invalidate,
  })
}

