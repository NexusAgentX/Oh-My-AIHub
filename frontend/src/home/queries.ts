import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'

export const pointsKeys = {
  summary: ['points', 'summary'] as const,
}

/** 当前账户的积分概况（余额、信用额度、可透支额度）。 */
export function usePoints() {
  return useQuery({ queryKey: pointsKeys.summary, queryFn: () => api.points() })
}
