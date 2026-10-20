import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiGet, apiSend, pathSegment } from '../api/client'
import type { ApiKeyCreateRequest, ApiKeyUpdateRequest, RoutingPreferenceInput } from '../api/types'

export const keyKeys = {
  all: ['keys'] as const,
  list: ['keys', 'list'] as const,
  detail: (id: string) => ['keys', 'detail', id] as const,
}

/** 每个用户未删除的 Key 上限。 */
export const maxKeys = 20

export function useKeys() {
  return useQuery({ queryKey: keyKeys.list, queryFn: () => apiGet<'listKeys'>('/api/keys') })
}

export function useKey(id: string | null) {
  return useQuery({
    queryKey: keyKeys.detail(id ?? ''),
    queryFn: () => apiGet<'getKey'>(`/api/keys/${pathSegment(id ?? '')}`),
    enabled: Boolean(id),
  })
}

/** 完整 Key：每次读取写入审计，不进缓存。 */
export async function fetchKeySecret(id: string) {
  return (await apiGet<'getKeySecret'>(`/api/keys/${pathSegment(id)}/secret`)).secret
}

function useInvalidateKeys() {
  const client = useQueryClient()
  return () =>
    Promise.all([
      client.invalidateQueries({ queryKey: keyKeys.all }),
      client.invalidateQueries({ queryKey: ['home'] }),
    ])
}

export function useCreateKey() {
  const invalidate = useInvalidateKeys()
  return useMutation({
    mutationFn: (input: ApiKeyCreateRequest) => apiSend<'createKey'>('POST', '/api/keys', input),
    onSuccess: invalidate,
  })
}

export function useUpdateKey(id: string) {
  const invalidate = useInvalidateKeys()
  return useMutation({
    mutationFn: (input: ApiKeyUpdateRequest) => apiSend<'updateKey'>('PATCH', `/api/keys/${pathSegment(id)}`, input),
    onSuccess: invalidate,
  })
}

export function useDeleteKey() {
  const invalidate = useInvalidateKeys()
  return useMutation({
    mutationFn: (id: string) => apiSend<'deleteKey'>('DELETE', `/api/keys/${pathSegment(id)}`),
    onSuccess: invalidate,
  })
}

/** Key 级单独路由：设置或删除（删除即回到跟随账号）。 */
export function useKeyRouting(keyId: string) {
  const invalidate = useInvalidateKeys()
  const set = useMutation({
    mutationFn: (input: { model: string; preference: RoutingPreferenceInput }) =>
      apiSend<'setKeyRouting'>('PUT', `/api/keys/${pathSegment(keyId)}/routing/${pathSegment(input.model)}`, input.preference),
    onSuccess: invalidate,
  })
  const remove = useMutation({
    mutationFn: (model: string) =>
      apiSend<'deleteKeyRouting'>('DELETE', `/api/keys/${pathSegment(keyId)}/routing/${pathSegment(model)}`),
    onSuccess: invalidate,
  })
  return { set, remove }
}
