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
  requestBody: { content: { 'application/json': infer B } }
}
  ? B
  : never

export type Account = Schemas['Account']
export type AccountStatus = Account['status']
export type Wallet = Schemas['Wallet']
export type WalletRiskStatus = Wallet['risk_status']
export type WalletRecoveryAction = Schemas['WalletResponse']['recovery_actions'][number]
export type LedgerCounterparty = Schemas['LedgerCounterparty']
export type LedgerEntry = Schemas['LedgerEntry']
export type LedgerMetrics = Schemas['LedgerMetrics']

export type PriceTier = Schemas['PriceTier']
export type CatalogModel = Schemas['Model']
export type CatalogModelStatus = CatalogModel['status']
export type ModelInput = Schemas['ModelRequest']
export type PriceTierInput = Schemas['PriceTierRequest']

export type Channel = Schemas['OwnerChannel']
export type ChannelStatus = Channel['status']
export type ChannelOffer = Schemas['OwnerOffer']
export type ChannelOfferStatus = ChannelOffer['status']
export type ChannelProtocol = ChannelOffer['protocol']
export type ChannelOfferInput = Schemas['OfferInput']
export type ValidationSummary = Schemas['ValidationSummary']
export type ValidationStatus = ValidationSummary['status']
export type AuthorizedValidationAttempt = Schemas['ValidationAttempt']
export type MarketOffer = Schemas['MarketOffer']
export type MarketChannel = Schemas['MarketChannel']
export type AdminChannel = Schemas['AdminChannel']
export type AdminChannelOffer = Schemas['AdminChannelOffer']

export type APIKey = Schemas['ApiKey']
export type APIKeyStatus = APIKey['status']
export type APIKeyPool = Schemas['ApiKeyPool']
export type APIKeyPoolMember = Schemas['ApiKeyPoolMember']
export type APIKeyPoolInput = Schemas['ApiPoolRequest']

export type GatewayUsage = Schemas['Usage']
export type GatewayAttempt = Schemas['GatewayAttempt']
export type GatewayAttemptStatus = GatewayAttempt['status']
export type GatewayCall = Schemas['GatewayCall']
export type GatewayCallStatus = GatewayCall['status']
export type GatewayDashboard = Schemas['Dashboard']

export type C2CPaymentMethod = Schemas['C2CPaymentMethod']
export type C2CPaymentMethodRequest = Schemas['C2CPaymentMethodRequest']
export type C2COrder = Schemas['C2COrder']
export type C2COrderStatus = C2COrder['status']
export type C2CPaymentMethodType = C2COrder['payment_types'][number]
export type C2CTrade = Schemas['C2CTrade']
export type C2CAdminTrade = Schemas['C2CAdminTrade']
/** 参与者与管理员共用的交易详情响应；仅管理员视图带信用冻结字段。 */
export type C2CTradeView = C2CTrade | C2CAdminTrade
export type C2CTradeStatus = C2CTrade['status']
export type C2CResolutionAction = Schemas['C2CResolveRequest']['action']
export type C2CMarket = Schemas['C2CMarket']

export type OpsMetrics = Schemas['OpsMetrics']
export type OpsAnomalies = Schemas['OpsAnomalies']
export type OpsInspection = Schemas['OpsInspection']
export type OpsTrialSummary = Schemas['OpsTrialSummary']
export type ProviderIncomeSnapshot = Schemas['OpsProviderIncome']
export type FeeRateVersion = Schemas['FeeRateVersion']
export type FeeRateSnapshot = Schemas['FeeRateHistory']
