import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'

/** 网关领域的 key 工厂：失效整个领域用 gatewayKeys.all。 */
export const gatewayKeys = {
  all: ['gateway'] as const,
  dashboard: () => [...gatewayKeys.all, 'dashboard'] as const,
}

export function useGatewayDashboardQuery() {
  return useQuery({
    queryKey: gatewayKeys.dashboard(),
    queryFn: () => api.gatewayDashboard(),
  })
}
