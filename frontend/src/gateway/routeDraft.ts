import type {
  APIKey,
  APIKeyPoolInput,
  APIKeyPoolMember,
  ChannelProtocol,
} from '../api/types'

/** 编辑中的路由：一把 Key 下「模型 + 原生协议」对应的渠道列表，顺序即备用顺序。 */
export type DraftRoute = APIKeyPoolInput & { draft_id: string }

export type APIKeyConflictState = {
  localVersion: number
  serverVersion: number | null
}

export function draftID(modelID: string, protocol: ChannelProtocol) {
  return `${modelID}\u0000${protocol}`
}

export function existingMemberMap(key: APIKey) {
  return Object.fromEntries(
    key.pools.flatMap((pool) =>
      pool.members.map((member) => [member.offer_id, member]),
    ),
  ) as Record<string, APIKeyPoolMember>
}

export function draftRoutesFromKey(key: APIKey): DraftRoute[] {
  return key.pools.map((pool) => ({
    draft_id: draftID(pool.model_id, pool.protocol),
    model_id: pool.model_id,
    protocol: pool.protocol,
    offer_ids: pool.members.map((member) => member.offer_id),
  }))
}

export function isAPIKeySaveBlocked(
  saving: boolean,
  conflict: APIKeyConflictState | null,
) {
  return saving || conflict !== null
}

export function moveOfferIDs(
  offerIDs: string[],
  offerID: string,
  nextIndex: number,
) {
  const currentIndex = offerIDs.indexOf(offerID)
  if (currentIndex < 0) return offerIDs
  const boundedIndex = Math.max(0, Math.min(nextIndex, offerIDs.length - 1))
  const reordered = [...offerIDs]
  reordered.splice(currentIndex, 1)
  reordered.splice(boundedIndex, 0, offerID)
  return reordered
}

/** 保存前校验；返回错误文案，通过时返回空串。 */
export function validateKeyDraft(displayName: string, routes: DraftRoute[]) {
  if (!displayName.trim()) return '请输入 Key 名称'
  if (routes.length === 0) return '至少配置一个路由'
  if (routes.some((route) => route.offer_ids.length === 0)) {
    return '每个路由至少需要一个渠道'
  }
  return ''
}

export function routeInputs(routes: DraftRoute[]): APIKeyPoolInput[] {
  return routes.map(({ model_id, protocol, offer_ids }) => ({
    model_id,
    protocol,
    offer_ids,
  }))
}
