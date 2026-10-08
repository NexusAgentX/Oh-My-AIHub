import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiGet, apiSend, pathSegment } from '../api/client'
import type { RequestBody } from '../api/types'

export const channelKeys = {
  all: ['channels'] as const,
  list: ['channels', 'list'] as const,
  detail: (id: string) => ['channels', 'detail', id] as const,
  stats: (id: string, from?: string) => ['channels', 'stats', id, from ?? ''] as const,
}

export function useChannels() {
  return useQuery({ queryKey: channelKeys.list, queryFn: () => apiGet<'listChannels'>('/api/channels') })
}

export function useChannel(id: string | null) {
  return useQuery({
    queryKey: channelKeys.detail(id ?? ''),
    queryFn: () => apiGet<'getChannel'>(`/api/channels/${pathSegment(id ?? '')}`),
    enabled: Boolean(id),
  })
}

export function useChannelStats(id: string, from?: string) {
  return useQuery({
    queryKey: channelKeys.stats(id, from),
    queryFn: () => apiGet<'getChannelStats'>(`/api/channels/${pathSegment(id)}/stats`, { from }),
  })
}

function useInvalidateChannels() {
  const client = useQueryClient()
  return () =>
    Promise.all([
      client.invalidateQueries({ queryKey: channelKeys.all }),
      client.invalidateQueries({ queryKey: ['models'] }),
      client.invalidateQueries({ queryKey: ['home'] }),
    ])
}

export function useDiscoverChannel() {
  return useMutation({
    mutationFn: (input: RequestBody<'discoverChannel'>) =>
      apiSend<'discoverChannel'>('POST', '/api/channels/discover', input),
  })
}

export function useCreateChannel() {
  const invalidate = useInvalidateChannels()
  return useMutation({
    mutationFn: async (input: RequestBody<'createChannel'>) =>
      (await apiSend<'createChannel'>('POST', '/api/channels', input)).channel,
    onSuccess: invalidate,
  })
}

export function useUpdateChannel() {
  const invalidate = useInvalidateChannels()
  return useMutation({
    mutationFn: async (input: { id: string; body: RequestBody<'updateChannel'> }) =>
      (await apiSend<'updateChannel'>('PATCH', `/api/channels/${pathSegment(input.id)}`, input.body)).channel,
    onSuccess: invalidate,
  })
}

export function useDeleteChannel() {
  const invalidate = useInvalidateChannels()
  return useMutation({
    mutationFn: (id: string) => apiSend<'deleteChannel'>('DELETE', `/api/channels/${pathSegment(id)}`),
    onSuccess: invalidate,
  })
}

export function useTestChannel() {
  const invalidate = useInvalidateChannels()
  return useMutation({
    mutationFn: (input: { id: string; body: RequestBody<'testChannel'> }) =>
      apiSend<'testChannel'>('POST', `/api/channels/${pathSegment(input.id)}/test`, input.body),
    onSuccess: invalidate,
  })
}
