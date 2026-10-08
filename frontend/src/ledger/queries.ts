import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { api } from '../api/client'

/** 管理员账本与运营领域的 key 工厂：失效整个领域用 adminLedgerKeys.all。 */
export const adminLedgerKeys = {
  all: ['admin-ledger'] as const,
  metrics: (hours: number) => [...adminLedgerKeys.all, 'metrics', hours] as const,
  providers: (hours: number) => [...adminLedgerKeys.all, 'providers', hours] as const,
  anomalies: () => [...adminLedgerKeys.all, 'anomalies'] as const,
  inspections: () => [...adminLedgerKeys.all, 'inspections'] as const,
  system: (kind: SystemKind) => [...adminLedgerKeys.all, 'system', kind] as const,
  feeRate: () => [...adminLedgerKeys.all, 'fee-rate'] as const,
  account: (accountID: string) => [...adminLedgerKeys.all, 'account', accountID] as const,
}

export type SystemKind = 'platform_incentive' | 'platform_loss'

/** 以查询发起时刻为终点、向前 hours 小时的窗口（key 只含 hours，避免每次渲染变化）。 */
export function windowBounds(hours: number) {
  const to = new Date()
  const from = new Date(to.getTime() - hours * 3600 * 1000)
  return { from: from.toISOString(), to: to.toISOString() }
}

export function useOpsMetricsQuery(hours: number) {
  return useQuery({
    queryKey: adminLedgerKeys.metrics(hours),
    queryFn: () => {
      const { from, to } = windowBounds(hours)
      return api.opsMetrics(from, to)
    },
  })
}

export function useProviderIncomeQuery(hours: number) {
  return useQuery({
    queryKey: adminLedgerKeys.providers(hours),
    queryFn: () => {
      const { from, to } = windowBounds(hours)
      return api.opsProviderIncome(from, to)
    },
  })
}

export function useOpsAnomaliesQuery() {
  return useQuery({
    queryKey: adminLedgerKeys.anomalies(),
    queryFn: () => api.opsAnomalies(),
  })
}

export function useOpsInspectionsQuery() {
  return useQuery({
    queryKey: adminLedgerKeys.inspections(),
    queryFn: () => api.opsInspections(10),
  })
}

/** 手动巡检后刷新整个运营领域（指标、异常、巡检记录）。 */
export function useRunInspection() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => api.runOpsInspection(),
    onSuccess: () => client.invalidateQueries({ queryKey: adminLedgerKeys.all }),
  })
}

/** 平台系统账户：余额与最近分录。 */
export function useSystemAccountQuery(kind: SystemKind) {
  return useQuery({
    queryKey: adminLedgerKeys.system(kind),
    queryFn: async () => {
      const [wallet, entries] = await Promise.all([
        api.adminSystemWallet(kind),
        api.adminSystemEntries(kind, '', 5),
      ])
      return { wallet, entries: entries.entries }
    },
  })
}

export function useFeeRatesQuery() {
  return useQuery({
    queryKey: adminLedgerKeys.feeRate(),
    queryFn: () => api.adminFeeRates(),
  })
}

export function useSetFeeRate() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: { expectedVersion: number; feeRate: string; reason: string }) =>
      api.setAdminFeeRate(input.expectedVersion, input.feeRate, input.reason),
    onSettled: () => client.invalidateQueries({ queryKey: adminLedgerKeys.feeRate() }),
  })
}

export function useAdminAccountWalletQuery(accountID: string) {
  return useQuery({
    queryKey: [...adminLedgerKeys.account(accountID), 'wallet'] as const,
    queryFn: () => api.adminAccountWallet(accountID),
  })
}

/** 指定账户的分录，按 next_before 游标分页追加。 */
export function useAdminAccountEntriesQuery(accountID: string) {
  return useInfiniteQuery({
    queryKey: [...adminLedgerKeys.account(accountID), 'entries'] as const,
    queryFn: ({ pageParam }) => api.adminAccountEntries(accountID, pageParam),
    initialPageParam: '',
    getNextPageParam: (last) => last.next_before || undefined,
  })
}
