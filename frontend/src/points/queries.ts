import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiGet, apiSend, pathSegment, request, withQuery, type QueryParams } from '../api/client'
import type { RequestBody } from '../api/types'
import type { PointsEntryPage, TradeStatus } from '../api/types'

export const pointsKeys = {
  all: ['points'] as const,
  summary: ['points', 'summary'] as const,
  entries: (params: QueryParams) => ['points', 'entries', params] as const,
}

export const c2cKeys = {
  all: ['c2c'] as const,
  orders: ['c2c', 'orders'] as const,
  myOrders: ['c2c', 'my-orders'] as const,
  myTrades: (params: QueryParams) => ['c2c', 'my-trades', params] as const,
  trade: (id: string) => ['c2c', 'trade', id] as const,
}

/** 当前账户的积分概况（余额、信用额度、可透支额度；G 补充走势与期间对账）。 */
export function usePoints() {
  return useQuery({ queryKey: pointsKeys.summary, queryFn: () => apiGet<'getPoints'>('/api/points') })
}

export type EntryParams = {
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
  })
}

/** 账单 CSV 导出（G）：先取回再下载，失败（如 501）时抛出可展示的错误。 */
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

export function useC2COrders() {
  return useQuery({
    queryKey: c2cKeys.orders,
    queryFn: () => apiGet<'listC2COrders'>('/api/c2c/orders', { limit: 100 }),
  })
}

export function useMyC2COrders() {
  return useQuery({
    queryKey: c2cKeys.myOrders,
    queryFn: () => apiGet<'listMyC2COrders'>('/api/c2c/my/orders', { limit: 100 }),
  })
}

export function useMyC2CTrades(params: { status?: TradeStatus; role?: 'buyer' | 'seller' }) {
  return useQuery({
    queryKey: c2cKeys.myTrades(params),
    queryFn: () => apiGet<'listMyC2CTrades'>('/api/c2c/my/trades', { ...params, limit: 100 }),
  })
}

export function useC2CTrade(id: string | null) {
  return useQuery({
    queryKey: c2cKeys.trade(id ?? ''),
    queryFn: async () => (await apiGet<'getC2CTrade'>(`/api/c2c/trades/${pathSegment(id ?? '')}`)).trade,
    enabled: Boolean(id),
    refetchInterval: 15_000,
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

