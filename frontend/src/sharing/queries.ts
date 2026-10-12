import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiGet, apiSend, pathSegment } from '../api/client'
import type { RequestBody } from '../api/types'

export function useSuppliers() {
  return useQuery({ queryKey: ['sharing', 'suppliers'], queryFn: async () => {
    const first = await apiGet<'listSuppliers'>('/api/suppliers', { limit: 100 })
    let cursor = first.next_cursor
    while (cursor) { const page = await apiGet<'listSuppliers'>('/api/suppliers', { limit: 100, cursor }); first.items.push(...page.items); cursor = page.next_cursor }
    return first.items
  } })
}
export function useSupplier(id: string) {
  return useQuery({ queryKey: ['sharing', 'supplier', id], queryFn: () => apiGet<'getSupplier'>(`/api/suppliers/${pathSegment(id)}`) })
}
export function useHall(filters: Record<string, string>) {
  return useInfiniteQuery({ queryKey: ['sharing', 'hall', filters], initialPageParam: '',
    queryFn: ({ pageParam }) => apiGet<'listChannelHall'>('/api/channel-hall', { ...filters, cursor: pageParam }),
    getNextPageParam: (page) => page.next_cursor ?? undefined })
}
export function useHallChannel(id: string) {
  return useQuery({ queryKey: ['sharing', 'channel', id], queryFn: () => apiGet<'getHallChannel'>(`/api/channel-hall/${pathSegment(id)}`), refetchInterval: 30_000 })
}
export function useAdminSuppliers() {
  return useQuery({ queryKey: ['sharing', 'admin'], queryFn: async () => {
    const first = await apiGet<'adminListSuppliers'>('/api/admin/suppliers', { limit: 100 })
    let cursor = first.next_cursor
    while (cursor) { const page = await apiGet<'adminListSuppliers'>('/api/admin/suppliers', { limit: 100, cursor }); first.items.push(...page.items); cursor = page.next_cursor }
    return first.items
  } })
}
export function useSaveSupplier() {
  const client = useQueryClient()
  return useMutation({ mutationFn: ({ id, body }: { id?: string; body: RequestBody<'createSupplier'> }) => id
    ? apiSend<'updateSupplier'>('PUT', `/api/admin/suppliers/${pathSegment(id)}`, body)
    : apiSend<'createSupplier'>('POST', '/api/admin/suppliers', body),
    onSuccess: () => client.invalidateQueries({ queryKey: ['sharing'] }) })
}
