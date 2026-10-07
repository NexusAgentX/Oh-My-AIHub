import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import { walletKeys } from '../wallet/queries'
import { isC2CTradeTerminal } from './presentation'

/** C2C 领域的 key 工厂：失效整个领域用 c2cKeys.all。 */
export const c2cKeys = {
  all: ['c2c'] as const,
  market: () => [...c2cKeys.all, 'market'] as const,
  order: (orderID: string) => [...c2cKeys.all, 'order', orderID] as const,
  activity: () => [...c2cKeys.all, 'activity'] as const,
  trade: (tradeID: string) => [...c2cKeys.all, 'trade', tradeID] as const,
}

/** 挂单与交易随对手方操作随时变化，进入页面总是重新取数。 */
const fresh = { staleTime: 0 } as const

export function useC2CMarketQuery() {
  return useQuery({ queryKey: c2cKeys.market(), queryFn: () => api.c2cMarket(), ...fresh })
}

export function useC2COrderQuery(orderID: string) {
  return useQuery({
    queryKey: c2cKeys.order(orderID),
    queryFn: () => api.c2cOrder(orderID),
    enabled: Boolean(orderID),
    ...fresh,
  })
}

export function useC2CActivityQuery() {
  return useQuery({ queryKey: c2cKeys.activity(), queryFn: () => api.c2cActivity(), ...fresh })
}

/** 交易详情：未结束时每 15 秒刷新一次，对方放行或取消后无需手动刷新。 */
export function useC2CTradeQuery(tradeID: string) {
  return useQuery({
    queryKey: c2cKeys.trade(tradeID),
    queryFn: () => api.c2cTrade(tradeID),
    enabled: Boolean(tradeID),
    refetchInterval: (query) =>
      query.state.data && !isC2CTradeTerminal(query.state.data.status) ? 15_000 : false,
    ...fresh,
  })
}

/** 写操作成功后：C2C 全部缓存与钱包（冻结、余额变化）一并失效。 */
function useInvalidateAfterWrite() {
  const queryClient = useQueryClient()
  return () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: c2cKeys.all }),
      queryClient.invalidateQueries({ queryKey: walletKeys.all }),
    ])
}

export function useCreateC2COrder() {
  const invalidate = useInvalidateAfterWrite()
  return useMutation({
    mutationFn: (input: Parameters<typeof api.createC2COrder>[0]) => api.createC2COrder(input),
    onSuccess: invalidate,
  })
}

export function useTakeC2COrder() {
  const invalidate = useInvalidateAfterWrite()
  return useMutation({
    mutationFn: (input: { orderID: string; quantity: string; paymentMethodID: string }) =>
      api.takeC2COrder(input.orderID, input.quantity, input.paymentMethodID),
    onSuccess: invalidate,
  })
}

export function useCancelC2COrder() {
  const invalidate = useInvalidateAfterWrite()
  return useMutation({
    mutationFn: (orderID: string) => api.cancelC2COrder(orderID),
    onSuccess: invalidate,
  })
}

export function useMarkC2CPaid() {
  const invalidate = useInvalidateAfterWrite()
  return useMutation({
    mutationFn: (input: { tradeID: string; paymentReference: string }) =>
      api.markC2CPaid(input.tradeID, input.paymentReference),
    onSuccess: invalidate,
  })
}

export function useCancelC2CTrade() {
  const invalidate = useInvalidateAfterWrite()
  return useMutation({
    mutationFn: (tradeID: string) => api.cancelC2CTrade(tradeID),
    onSuccess: invalidate,
  })
}

export function useReleaseC2CTrade() {
  const invalidate = useInvalidateAfterWrite()
  return useMutation({
    mutationFn: (tradeID: string) => api.releaseC2CTrade(tradeID),
    onSuccess: invalidate,
  })
}

/** 开启争议或补充陈述（append 为 true），只提交文字。 */
export function useSubmitC2CDispute() {
  const invalidate = useInvalidateAfterWrite()
  return useMutation({
    mutationFn: (input: { tradeID: string; statement: string; append: boolean }) =>
      api.submitC2CDispute(input.tradeID, input.statement, input.append),
    onSuccess: invalidate,
  })
}
