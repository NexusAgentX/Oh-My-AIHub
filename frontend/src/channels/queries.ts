import { useQuery } from '@tanstack/react-query'
import { apiGet, pathSegment } from '../api/client'
import { usePageVisible } from '../api/visibility'

export const channelKeys = { stats: (id: string, from?: string) => ['channels', 'stats', id, from ?? ''] as const }

export function useChannelStats(id: string, from?: string) {
  const visible = usePageVisible()
  return useQuery({
    queryKey: channelKeys.stats(id, from),
    queryFn: () => apiGet<'getChannelStats'>(`/api/channels/${pathSegment(id)}/stats`, { from }),
    refetchOnWindowFocus: true,
    refetchInterval: visible ? 60_000 : false,
  })
}
