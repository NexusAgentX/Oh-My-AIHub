import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import type { AccountStatus } from '../api/types'
import { adminLedgerKeys } from '../ledger/queries'

/** 管理员账户领域的 key 工厂：失效整个领域用 adminAccountKeys.all。 */
export const adminAccountKeys = {
  all: ['admin-accounts'] as const,
  list: (search: string) => [...adminAccountKeys.all, 'list', search] as const,
  metrics: () => [...adminAccountKeys.all, 'metrics'] as const,
}

export function useAdminAccountsQuery(search: string) {
  return useQuery({
    queryKey: adminAccountKeys.list(search),
    queryFn: () => api.accounts(search),
  })
}

export function useAccountMetricsQuery() {
  return useQuery({
    queryKey: adminAccountKeys.metrics(),
    queryFn: () => api.ledgerMetrics(),
  })
}

/** 账户变更会影响列表、信用指标与运营台的账本指标。 */
function useInvalidateAccounts() {
  const client = useQueryClient()
  return () =>
    Promise.all([
      client.invalidateQueries({ queryKey: adminAccountKeys.all }),
      client.invalidateQueries({ queryKey: adminLedgerKeys.all }),
    ])
}

export function useCreateAccount() {
  const invalidate = useInvalidateAccounts()
  return useMutation({
    mutationFn: (input: {
      username: string
      display_name: string
      credit_limit: string
      is_admin: boolean
      status: AccountStatus
    }) => api.createAccount(input),
    onSuccess: invalidate,
  })
}

export function useUpdateAccount() {
  const invalidate = useInvalidateAccounts()
  return useMutation({
    mutationFn: (input: {
      accountID: string
      expectedVersion: number
      patch: {
        status?: AccountStatus
        credit_limit?: string
        credit_frozen?: boolean
        is_admin?: boolean
      }
    }) => api.updateAccount(input.accountID, input.expectedVersion, input.patch),
    onSettled: invalidate,
  })
}

/** 重置密码：新初始密码只在调用方的组件状态中短暂持有，不进入查询缓存。 */
export function useResetAccountPassword() {
  return useMutation({
    mutationFn: (accountID: string) => api.resetAccountPassword(accountID),
  })
}
