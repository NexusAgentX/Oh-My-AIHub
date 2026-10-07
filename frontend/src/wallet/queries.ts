import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'

/** 钱包领域的 key 工厂：失效整个领域用 walletKeys.all。 */
export const walletKeys = {
  all: ['wallet'] as const,
  summary: () => [...walletKeys.all, 'summary'] as const,
  entries: () => [...walletKeys.all, 'entries'] as const,
}

/** 钱包摘要与补足积分入口；未登录或需先改密时不请求。 */
export function useWalletQuery() {
  const { account } = useAuth()
  const enabled = Boolean(account) && !account?.must_change_password
  return useQuery({
    queryKey: walletKeys.summary(),
    queryFn: () => api.wallet(),
    enabled,
  })
}

/** 账本分录，按 next_before 游标分页追加。 */
export function useLedgerEntriesQuery() {
  return useInfiniteQuery({
    queryKey: walletKeys.entries(),
    queryFn: ({ pageParam }) => api.walletEntries(pageParam),
    initialPageParam: '',
    getNextPageParam: (last) => last.next_before || undefined,
  })
}

/**
 * 供外壳顶栏、工作台、钱包等页面共用的钱包视图：
 * 与旧 WalletProvider 保持同一形状，但数据由 TanStack Query 缓存与去重。
 */
export function useWallet() {
  const query = useWalletQuery()
  return {
    wallet: query.data?.wallet ?? null,
    recoveryActions: query.data?.recovery_actions ?? [],
    loading: query.isPending && query.fetchStatus !== 'idle',
    error: query.isError ? errorMessage(query.error, '钱包加载失败') : '',
    refresh: async () => {
      await query.refetch()
    },
  }
}
