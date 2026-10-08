import {
  keepPreviousData,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import type { RequestBody } from '../api/types'
import { adminApi, type AdminQuery } from './api'

export const adminKeys = {
  all: ['admin'] as const,
  overview: ['admin', 'overview'] as const,
  points: (days: number) => ['admin', 'points', days] as const,
  accounts: (query: object) => ['admin', 'accounts', query] as const,
  models: ['admin', 'models'] as const,
  settings: ['admin', 'settings'] as const,
  audit: (query: object) => ['admin', 'audit', query] as const,
  channels: (query: object) => ['admin', 'channels', query] as const,
  channel: (channelID: string) => ['admin', 'channel', channelID] as const,
  disputes: (query: object) => ['admin', 'disputes', query] as const,
  trade: (tradeID: string) => ['admin', 'trade', tradeID] as const,
  calls: (query: object) => ['admin', 'calls', query] as const,
  transactions: (query: object) => ['admin', 'transactions', query] as const,
  transaction: (transactionID: string) => ['admin', 'transaction', transactionID] as const,
}

type Page<T> = { items: T[]; next_cursor: string | null }

/** 游标分页列表：pages 展平为 items，hasMore / loadMore 交给「加载更多」按钮。 */
function usePagedQuery<T>(
  queryKey: readonly unknown[],
  fetchPage: (cursor: string | undefined) => Promise<Page<T>>,
  enabled = true,
) {
  const query = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam }) => fetchPage(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    placeholderData: keepPreviousData,
    enabled,
  })
  return {
    ...query,
    data: query.data ? query.data.pages.flatMap((page) => page.items) : undefined,
  }
}

export function useAdminOverview() {
  return useQuery({ queryKey: adminKeys.overview, queryFn: adminApi.overview })
}

/** 积分全局；days 为走势时间窗（7 / 30 / 90），切换时保留上一份数据避免整页闪烁。 */
export function useAdminPoints(days: 7 | 30 | 90 = 30) {
  return useQuery({
    queryKey: adminKeys.points(days),
    queryFn: () => adminApi.points(days),
    placeholderData: keepPreviousData,
  })
}

export function useAdminAccounts(query: Omit<AdminQuery<'listAdminAccounts'>, 'cursor'>) {
  return usePagedQuery(adminKeys.accounts(query), (cursor) =>
    adminApi.accounts({ ...query, cursor, limit: 50 }),
  )
}

export function useAdminModels() {
  return useQuery({ queryKey: adminKeys.models, queryFn: adminApi.models })
}

export function useAdminSettings() {
  return useQuery({ queryKey: adminKeys.settings, queryFn: adminApi.settings })
}

export function useAdminAudit(query: Omit<AdminQuery<'listAdminAudit'>, 'cursor'>, enabled = true) {
  return usePagedQuery(
    adminKeys.audit(query),
    (cursor) => adminApi.audit({ ...query, cursor }),
    enabled,
  )
}

export function useAdminChannels(query: Omit<AdminQuery<'listAdminChannels'>, 'cursor'>) {
  return usePagedQuery(adminKeys.channels(query), (cursor) =>
    adminApi.channels({ ...query, cursor, limit: 50 }),
  )
}

export function useAdminChannel(channelID: string) {
  return useQuery({
    queryKey: adminKeys.channel(channelID),
    queryFn: () => adminApi.channel(channelID),
  })
}

export function useAdminDisputes(query: Omit<AdminQuery<'listAdminDisputes'>, 'cursor'>) {
  return usePagedQuery(adminKeys.disputes(query), (cursor) =>
    adminApi.disputes({ ...query, cursor }),
  )
}

export function useAdminTrade(tradeID: string) {
  return useQuery({ queryKey: adminKeys.trade(tradeID), queryFn: () => adminApi.trade(tradeID) })
}

export function useAdminCalls(query: Omit<AdminQuery<'listAdminCalls'>, 'cursor'>, enabled = true) {
  const paged = usePagedQuery(
    adminKeys.calls(query),
    (cursor) => adminApi.calls({ ...query, cursor }),
    enabled,
  )
  return paged
}

export function useAdminTransactions(
  query: Omit<AdminQuery<'listAdminLedgerTransactions'>, 'cursor'>,
  enabled = true,
) {
  return usePagedQuery(
    adminKeys.transactions(query),
    (cursor) => adminApi.transactions({ ...query, cursor }),
    enabled,
  )
}

export function useAdminTransaction(transactionID: string | null) {
  return useQuery({
    queryKey: adminKeys.transaction(transactionID ?? ''),
    queryFn: () => adminApi.transaction(transactionID ?? ''),
    enabled: Boolean(transactionID),
  })
}

/** 写操作成功后整体失效管理后台缓存：后台数据互相关联（余额、审计、概览）。 */
function useAdminMutation<TInput, TResult>(mutationFn: (input: TInput) => Promise<TResult>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn,
    onSuccess: () => client.invalidateQueries({ queryKey: adminKeys.all }),
  })
}

export function useCreateAccount() {
  return useAdminMutation((body: RequestBody<'createAdminAccount'>) => adminApi.createAccount(body))
}

export function useUpdateAccount() {
  return useAdminMutation((input: { id: string; body: RequestBody<'updateAdminAccount'> }) =>
    adminApi.updateAccount(input.id, input.body),
  )
}

export function useResetPassword() {
  return useAdminMutation((accountID: string) => adminApi.resetPassword(accountID))
}

export function useAdjustAccount() {
  return useAdminMutation(
    (input: { id: string; body: RequestBody<'adjustAdminAccount'>; idempotencyKey: string }) =>
      adminApi.adjust(input.id, input.body, input.idempotencyKey),
  )
}

export function useWriteOffAccount() {
  return useAdminMutation(
    (input: { id: string; body: RequestBody<'writeOffAdminAccount'>; idempotencyKey: string }) =>
      adminApi.writeOff(input.id, input.body, input.idempotencyKey),
  )
}

export function useDeleteModel() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: adminApi.deleteModel,
    onSuccess: () => client.invalidateQueries(),
  })
}

export function useSaveModel() {
  return useAdminMutation(
    (
      input:
        | { mode: 'create'; body: RequestBody<'createAdminModel'> }
        | { mode: 'update'; id: string; body: RequestBody<'updateAdminModel'> },
    ) =>
      input.mode === 'create'
        ? adminApi.createModel(input.body)
        : adminApi.updateModel(input.id, input.body),
  )
}

export function useUpdateSettings() {
  return useAdminMutation((body: RequestBody<'updateAdminSettings'>) => adminApi.updateSettings(body))
}

export function useChannelSuspension() {
  return useAdminMutation((input: { id: string; suspend: boolean; reason: string }) =>
    input.suspend
      ? adminApi.suspendChannel(input.id, input.reason)
      : adminApi.unsuspendChannel(input.id, input.reason),
  )
}

export function useResolveTrade() {
  return useAdminMutation((input: { id: string; body: RequestBody<'resolveAdminC2CTrade'> }) =>
    adminApi.resolveTrade(input.id, input.body),
  )
}

export function useRepairCall() {
  return useAdminMutation((input: { id: string; body: RequestBody<'repairAdminLedgerCall'> }) =>
    adminApi.repairCall(input.id, input.body),
  )
}
