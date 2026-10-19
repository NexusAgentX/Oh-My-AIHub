import { usePageVisible } from '../api/visibility'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiGet, apiSend } from '../api/client'
import type { components } from '../api/schema.gen'
export type TypicalConfig = components['schemas']['TypicalConfig']
export type TypicalEstimate = components['schemas']['TypicalEstimate']
export function useTypicalCosts(level = 'group', model = '', cursor = '') {
  const visible = usePageVisible()
  return useQuery({ queryKey: ['typical-costs', level, model, cursor], queryFn: () => apiGet<'listTypicalCosts'>('/api/typical-costs', { level, model_id: model, cursor, limit: 20 }), refetchInterval: (query) => visible && (query.state.data?.status.running || query.state.data?.status.pending) ? 3000 : false })
}
export function useTypicalSettings() {
  return useQuery({ queryKey: ['typical-settings'], queryFn: () => apiGet<'getTypicalSettings'>('/api/admin/typical-costs/settings') })
}
export function useUpdateTypicalSettings() {
  const client = useQueryClient()
  return useMutation({ mutationFn: (body: TypicalConfig) => apiSend<'updateTypicalSettings'>('PUT', '/api/admin/typical-costs/settings', body), onSuccess: async (data) => { client.setQueryData(['typical-settings'], data); await client.invalidateQueries({ queryKey: ['typical-costs'] }) } })
}
export function useRefreshTypicalCosts() {
  const client = useQueryClient()
  return useMutation({ mutationFn: () => apiSend<'refreshTypicalCosts'>('POST', '/api/admin/typical-costs/refresh'), onSuccess: () => client.invalidateQueries({ queryKey: ['typical-costs'] }) })
}
