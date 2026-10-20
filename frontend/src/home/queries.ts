import { useQuery } from '@tanstack/react-query'
import { apiGet } from '../api/client'

export const homeKeys = {
  summary: ['home', 'summary'] as const,
}

/** 首页聚合：余额、今日、渠道、默认 Key、待处理交易、最近调用。 */
export function useHome() {
  return useQuery({ queryKey: homeKeys.summary, queryFn: () => apiGet<'getHome'>('/api/home') })
}
