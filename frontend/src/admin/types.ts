import type { components } from '../api/schema.gen'

// 管理后台用到的契约类型别名；结构唯一来源是 schema.gen.ts。

type Schemas = components['schemas']

export type AdminAccount = Schemas['AdminAccount']
export type Settings = Schemas['Settings']
export type SettingsUpdateRequest = Schemas['SettingsUpdateRequest']
export type AuditEntry = Schemas['AuditEntry']
export type ChannelEvent = Schemas['ChannelEvent']
export type ChannelStatus = Schemas['GroupChannel']['status']
export type C2CTrade = Schemas['C2CTrade']
export type TradeStatus = Schemas['TradeStatus']
export type AdminOverview = Schemas['AdminOverview']
export type AttentionItem = Schemas['AttentionItem']
export type AdminPoints = Schemas['AdminPoints']
export type AdminPointsBalances = Schemas['AdminPointsBalances']
export type AdminPointsRisk = Schemas['AdminPointsRisk']
export type LedgerCheck = Schemas['LedgerCheck']
export type LedgerChecks = Schemas['LedgerChecks']
export type PriceSnapshot = Schemas['PriceSnapshot']
export type LedgerTransaction = Schemas['LedgerTransaction']
export type LedgerAccountRef = Schemas['LedgerAccountRef']
export type LedgerRelated = Schemas['LedgerRelated']
export type TransactionType = Schemas['TransactionType']
export type AdminCall = Schemas['AdminCall']
export type CallOutcome = Schemas['CallOutcome']
export type ApiFormat = Schemas['Format']
