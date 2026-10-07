import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { api } from '../api/client'
import type { ChannelProtocol, MarketOffer } from '../api/types'

export type MarketSort =
  | 'input_price'
  | 'output_price'
  | 'cache_write_price'
  | 'cache_read_price'
  | 'rating'
  | 'success_rate'
  | 'ttft'
  | 'tps'

export type MarketFilters = {
  modelID: string
  protocol: ChannelProtocol | ''
  owner: string
  sort: MarketSort
}

/** API 市场与模型目录的 key 工厂；评分与加入路由后按 marketKeys.all 失效。 */
export const marketKeys = {
  all: ['market'] as const,
  models: () => [...marketKeys.all, 'models'] as const,
  offers: (filters: MarketFilters) => [...marketKeys.all, 'offers', filters] as const,
  routeOffers: (modelID: string, protocol: ChannelProtocol | '') =>
    [...marketKeys.all, 'route-offers', modelID, protocol] as const,
  allOffers: () => [...marketKeys.all, 'all-offers'] as const,
  channel: (channelID: string) => [...marketKeys.all, 'channel', channelID] as const,
}

/** 可用于路由的模型目录（启用的公开模型）。 */
export function useCatalogModelsQuery() {
  return useQuery({
    queryKey: marketKeys.models(),
    queryFn: () => api.models(),
  })
}

/** 市场报价，按 next_after 游标分页追加。 */
export function useMarketOffersQuery(filters: MarketFilters) {
  return useInfiniteQuery({
    queryKey: marketKeys.offers(filters),
    queryFn: ({ pageParam }) =>
      api.marketOffers({ ...filters, after: pageParam, limit: 20 }),
    initialPageParam: '',
    getNextPageParam: (last) => last.next_after || undefined,
  })
}

/** 某个模型 + 协议下可选的报价（快速开始使用，取价格最低的前 50 个）。 */
export function useRouteOffersQuery(modelID: string, protocol: ChannelProtocol) {
  return useQuery({
    queryKey: marketKeys.routeOffers(modelID, protocol),
    queryFn: async () =>
      (await api.marketOffers({ modelID, protocol, sort: 'input_price', limit: 50 })).offers,
    enabled: Boolean(modelID),
  })
}

/** 市场中全部报价（最多 2000 条），用于路由编辑时选择候选渠道。 */
export function useAllMarketOffersQuery(enabled = true) {
  return useQuery({
    queryKey: marketKeys.allOffers(),
    enabled,
    queryFn: async () => {
      const offers: MarketOffer[] = []
      let after = ''
      for (let page = 0; page < 20; page += 1) {
        const result = await api.marketOffers({ after, limit: 100 })
        offers.push(...result.offers)
        if (!result.next_after) break
        after = result.next_after
      }
      return [...new Map(offers.map((offer) => [offer.offer_id, offer])).values()]
    },
  })
}

export function useMarketChannelQuery(channelID: string) {
  return useQuery({
    queryKey: marketKeys.channel(channelID),
    queryFn: () => api.marketChannel(channelID),
    enabled: Boolean(channelID),
  })
}

/** 1～5 分评分；成功后用返回的渠道更新缓存并失效市场列表中的评分。 */
export function useRateChannelMutation(channelID: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (score: number) => api.rateChannel(channelID, score),
    onSuccess: (channel) => {
      client.setQueryData(marketKeys.channel(channelID), channel)
      void client.invalidateQueries({ queryKey: [...marketKeys.all, 'offers'] })
    },
  })
}
