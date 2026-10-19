import { usePageVisible } from '../api/visibility'
import { formDefaultsKey } from '../settings/queries'
import { secretSession } from '../ui/secretHandoff'
import { handoffAdminSecret } from './secret'
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
  account: (id: string) => ['admin', 'account', id] as const,
  settings: ['admin', 'settings'] as const,
  audit: (query: object) => ['admin', 'audit', query] as const,
  trades: (query: object) => ['admin', 'trades', query] as const,
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
    getNextPageParam: (last, pages) => pages.length < 10 ? last.next_cursor ?? undefined : undefined,
    placeholderData: keepPreviousData,
    enabled,
    refetchOnWindowFocus: true,
  })
  return {
    ...query,
    limited: query.data?.pages.length === 10 && Boolean(query.data.pages.at(-1)?.next_cursor),
    data: query.data ? query.data.pages.flatMap((page) => page.items) : undefined,
  }
}

export function useAdminOverview() {
  const visible = usePageVisible()
  return useQuery({ queryKey: adminKeys.overview, queryFn: adminApi.overview, refetchOnWindowFocus: true, refetchInterval: visible ? 60_000 : false })
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
export function useAdminAccount(id: string) {
  return useQuery({ queryKey: adminKeys.account(id), queryFn: () => adminApi.account(id) })
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



export function useAdminTrades(query: Omit<AdminQuery<'listAdminC2CTrades'>, 'cursor'>) {
  return usePagedQuery(adminKeys.trades(query), (cursor) =>
    adminApi.trades({ ...query, cursor }),
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
  return useAdminMutation(async (body: RequestBody<'createAdminAccount'>) => {
    const session = secretSession(), result = await adminApi.createAccount(body)
    handoffAdminSecret(result.account.id, result.initial_password, session)
    return { account: result.account }
  })
}

export function useUpdateAccount() {
  return useAdminMutation((input: { id: string; body: RequestBody<'updateAdminAccount'> }) =>
    adminApi.updateAccount(input.id, input.body),
  )
}

export function useResetPassword() {
  return useAdminMutation(async (accountID: string) => {
    const session = secretSession(), result = await adminApi.resetPassword(accountID)
    handoffAdminSecret(result.account.id, result.initial_password, session)
    return { account: result.account }
  })
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



export function useUpdateSettings() {
  const client = useQueryClient()
  return useAdminMutation(async (body: RequestBody<'updateAdminSettings'>) => {
    const result = await adminApi.updateSettings(body)
    await client.invalidateQueries({ queryKey: formDefaultsKey })
    return result
  })
}

export function useSyncExchangeRate() {
  return useAdminMutation(() => adminApi.syncExchangeRate())
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
