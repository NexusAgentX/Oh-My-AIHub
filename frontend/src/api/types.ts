import type { components, operations } from './schema.gen'

// API 类型的唯一来源是由 backend/api/openapi.yaml 生成的 schema.gen.ts。
// 此文件只提供简洁别名，不重新描述任何 JSON 结构。

type Schemas = components['schemas']

/** 某个 operationId 的 2xx JSON 响应体。 */
export type ResponseBody<Op extends keyof operations> = operations[Op] extends {
  responses: infer R
}
  ? {
      [S in keyof R]: S extends 200 | 201
        ? R[S] extends { content: { 'application/json': infer B } }
          ? B
          : never
        : never
    }[keyof R]
  : never

/** 某个 operationId 的 JSON 请求体。 */
export type RequestBody<Op extends keyof operations> = operations[Op] extends {
  requestBody?: { content: { 'application/json': infer B } }
}
  ? B
  : never

export type Account = Schemas['Account']
export type AccountStatus = Account['status']
export type Points = Schemas['Points']
export type PointsSummary = Schemas['PointsSummary']
export type PointsEntry = Schemas['PointsEntry']
export type PointsEntryPage = Schemas['PointsEntryPage']
export type TransactionType = Schemas['TransactionType']
export type HomeResponse = Schemas['HomeResponse']

export type Format = Schemas['Format']
export type RoutingMode = Schemas['RoutingMode']
export type CallOutcome = Schemas['CallOutcome']
export type ModelPrices = Schemas['ModelPrices']
export type EffectivePrices = Schemas['EffectivePrices']
export type PriceTier = Schemas['PriceTier']
export type CatalogModel = Schemas['CatalogModel']
export type ModelChannel = Schemas['ModelChannel']
export type ModelDetail = Schemas['ModelDetail']
export type RoutingPreference = Schemas['RoutingPreference']
export type RoutingPreferenceInput = Schemas['RoutingPreferenceInput']

export type ApiKey = Schemas['ApiKey']
export type ApiKeyDetail = Schemas['ApiKeyDetail']
export type ApiKeyCreateRequest = Schemas['ApiKeyCreateRequest']
export type ApiKeyUpdateRequest = Schemas['ApiKeyUpdateRequest']

export type Channel = Schemas['Channel']
export type ChannelStatus = Schemas['ChannelStatus']
export type ChannelModel = Schemas['ChannelModel']
export type ChannelModelInput = Schemas['ChannelModelInput']
export type ChannelAdvanced = Schemas['ChannelAdvanced']
export type ChannelAdvancedInput = Schemas['ChannelAdvancedInput']
export type ChannelEvent = Schemas['ChannelEvent']
export type ChannelDetail = Schemas['ChannelDetail']
export type ChannelStats = Schemas['ChannelStats']
export type ChannelCall = Schemas['ChannelCall']
export type DiscoveredModel = Schemas['DiscoveredModel']
export type ChannelTestResult = Schemas['ChannelTestResult']
export type FormatTest = Schemas['FormatTest']

export type CallSummary = Schemas['CallSummary']
export type CallDetail = Schemas['CallDetail']
export type CallAttempt = Schemas['CallAttempt']
export type CallStats = Schemas['CallStats']
export type CallPage = Schemas['CallPage']
export type UsageReport = Schemas['UsageReport']
export type UsageRow = Schemas['UsageRow']

export type PaymentMethod = Schemas['PaymentMethod']
export type C2COrder = Schemas['C2COrder']
export type C2CMyOrder = Schemas['C2CMyOrder']
export type C2CTrade = Schemas['C2CTrade']
export type TradeStatus = Schemas['TradeStatus']
export type Usage = Schemas['Usage']
export type ChannelCallPage = Schemas['ChannelCallPage']
export type DefaultKey = Schemas['DefaultKey']
