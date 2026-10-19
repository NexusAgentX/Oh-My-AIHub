import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiGet, apiSend } from '../api/client'
import type { components } from '../api/schema.gen'
import { usePageVisible } from '../api/visibility'

export function probing(channel: GroupChannel) { return channel.selected_address_ids.length > 0 && channel.status !== 'suspended' && channel.models.some(model => model.enabled && !model.excluded && model.probe_state === 'pending') }

export type GroupChannel = components['schemas']['GroupChannel']
export type GroupSource = components['schemas']['GroupChannelSource']
export type Overrides = components['schemas']['MarketDefaults']
export type ChannelCreate = components['schemas']['GroupChannelCreate']
export type ChannelUpdate = components['schemas']['GroupChannelUpdate']
export type Choice = components['schemas']['GroupChannelChoice']

export function useGroupChannels(admin = false) {
  const visible = usePageVisible()
  return useQuery({ queryKey: ['group-channels', 'list', admin], queryFn: () => admin ? apiGet<'listAdminGroupChannels'>('/api/admin/group-channels') : apiGet<'listGroupChannels'>('/api/group-channels'), refetchOnWindowFocus: true, refetchInterval: query => visible && query.state.data?.items.some(probing) ? 5000 : false })
}
export function useGroupChannel(id: string, admin = false) {
  const visible = usePageVisible()
  return useQuery({ queryKey: ['group-channels', 'detail', id, admin], queryFn: () => admin ? apiGet<'getAdminGroupChannel'>(`/api/admin/group-channels/${id}`) : apiGet<'getGroupChannel'>(`/api/group-channels/${id}`), enabled: Boolean(id), refetchOnWindowFocus: true, refetchInterval: query => visible && query.state.data && probing(query.state.data) ? 5000 : false })
}
export function useGroupSources() {
  return useQuery({ queryKey: ['group-channels', 'sources'], queryFn: () => apiGet<'groupChannelSources'>('/api/group-channels/sources') })
}
export function useSaveGroupChannel(id?: string) {
  const client = useQueryClient()
  return useMutation({ mutationFn: (body: ChannelCreate | ChannelUpdate) => id ? apiSend<'updateGroupChannel'>('PATCH', `/api/group-channels/${id}`, body as ChannelUpdate) : apiSend<'createGroupChannel'>('POST', '/api/group-channels', body as ChannelCreate), onSuccess: async (data) => { client.setQueryData(['group-channels', 'detail', data.id, false], data); await Promise.all([client.invalidateQueries({ queryKey: ['group-channels'] }),client.invalidateQueries({queryKey:['market']})]) } })
}
export function useGroupAction(id: string) {
  const client = useQueryClient()
  return useMutation({ mutationFn: async (value: { action: 'delete' | 'test' | 'moderate'; revision: number; reason?: string }) => {
    if (value.action === 'delete') return apiSend<'deleteGroupChannel'>('DELETE', `/api/group-channels/${id}`, { revision: value.revision })
    if (value.action === 'test') return apiSend<'retestGroupChannel'>('POST', `/api/group-channels/${id}/test`, { revision: value.revision })
    return apiSend<'moderateGroupChannel'>('POST', `/api/admin/group-channels/${id}/moderate`, { reason: value.reason ?? '' })
  }, onSuccess: () => Promise.all([client.invalidateQueries({ queryKey: ['group-channels'] }),client.invalidateQueries({queryKey:['market']})]) })
}

export function useGroupEvents(id: string) {
 return useQuery({queryKey:['group-channels','events',id],queryFn:()=>apiGet<'groupChannelEvents'>(`/api/group-channels/${id}/events`),enabled:Boolean(id)})
}
