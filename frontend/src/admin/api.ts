import { request } from '../api/client'
import type { operations } from '../api/schema.gen'
import type { RequestBody, ResponseBody } from '../api/types'

type QueryOf<Op extends keyof operations> = operations[Op]['parameters'] extends {
  query?: infer Q
}
  ? NonNullable<Q>
  : never

/** 拼接查询串：跳过 undefined 与空字符串。 */
export function withQuery(path: string, params: Record<string, string | number | undefined>) {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === '') continue
    search.set(key, String(value))
  }
  const text = search.toString()
  return text ? `${path}?${text}` : path
}

function post<Op extends keyof operations>(path: string, body: RequestBody<Op>, headers?: HeadersInit) {
  return request<ResponseBody<Op>>(path, { method: 'POST', body: JSON.stringify(body), headers })
}

function patch<Op extends keyof operations>(path: string, body: RequestBody<Op>) {
  return request<ResponseBody<Op>>(path, { method: 'PATCH', body: JSON.stringify(body) })
}

const id = encodeURIComponent

/** 管理员接口。路径与结构以 backend/api/openapi.yaml 为准。 */
export const adminApi = {
  overview: () => request<ResponseBody<'getAdminOverview'>>('/api/admin/overview'),
  points: (days?: number) => request<ResponseBody<'getAdminPoints'>>(withQuery('/api/admin/points', { days })),

  accounts: (query: QueryOf<'listAdminAccounts'>) =>
    request<ResponseBody<'listAdminAccounts'>>(withQuery('/api/admin/accounts', query)),
  createAccount: (body: RequestBody<'createAdminAccount'>) =>
    post<'createAdminAccount'>('/api/admin/accounts', body),
  updateAccount: (accountID: string, body: RequestBody<'updateAdminAccount'>) =>
    patch<'updateAdminAccount'>(`/api/admin/accounts/${id(accountID)}`, body),
  resetPassword: (accountID: string) =>
    request<ResponseBody<'resetAdminAccountPassword'>>(
      `/api/admin/accounts/${id(accountID)}/reset-password`,
      { method: 'POST' },
    ),
  adjust: (accountID: string, body: RequestBody<'adjustAdminAccount'>, idempotencyKey: string) =>
    post<'adjustAdminAccount'>(`/api/admin/accounts/${id(accountID)}/adjust`, body, {
      'Idempotency-Key': idempotencyKey,
    }),
  writeOff: (accountID: string, body: RequestBody<'writeOffAdminAccount'>, idempotencyKey: string) =>
    post<'writeOffAdminAccount'>(`/api/admin/accounts/${id(accountID)}/write-off`, body, {
      'Idempotency-Key': idempotencyKey,
    }),

  models: () => request<ResponseBody<'listAdminModels'>>('/api/admin/models'),
  createModel: (body: RequestBody<'createAdminModel'>) =>
    post<'createAdminModel'>('/api/admin/models', body),
  updateModel: (modelID: string, body: RequestBody<'updateAdminModel'>) =>
    patch<'updateAdminModel'>(`/api/admin/models/${id(modelID)}`, body),

  settings: () => request<ResponseBody<'getAdminSettings'>>('/api/admin/settings'),
  updateSettings: (body: RequestBody<'updateAdminSettings'>) =>
    request<ResponseBody<'updateAdminSettings'>>('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  audit: (query: QueryOf<'listAdminAudit'>) =>
    request<ResponseBody<'listAdminAudit'>>(withQuery('/api/admin/audit', query)),

  channels: (query: QueryOf<'listAdminChannels'>) =>
    request<ResponseBody<'listAdminChannels'>>(withQuery('/api/admin/channels', query)),
  channel: (channelID: string) =>
    request<ResponseBody<'getAdminChannel'>>(`/api/admin/channels/${id(channelID)}`),
  suspendChannel: (channelID: string, reason: string) =>
    post<'suspendAdminChannel'>(`/api/admin/channels/${id(channelID)}/suspend`, { reason }),
  unsuspendChannel: (channelID: string, reason: string) =>
    post<'unsuspendAdminChannel'>(`/api/admin/channels/${id(channelID)}/unsuspend`, { reason }),

  disputes: (query: QueryOf<'listAdminDisputes'>) =>
    request<ResponseBody<'listAdminDisputes'>>(withQuery('/api/admin/disputes', query)),
  trade: (tradeID: string) => request<ResponseBody<'getC2CTrade'>>(`/api/c2c/trades/${id(tradeID)}`),
  resolveTrade: (tradeID: string, body: RequestBody<'resolveAdminC2CTrade'>) =>
    post<'resolveAdminC2CTrade'>(`/api/admin/c2c/trades/${id(tradeID)}/resolve`, body),

  calls: (query: QueryOf<'listAdminCalls'>) =>
    request<ResponseBody<'listAdminCalls'>>(withQuery('/api/admin/calls', query)),
  transactions: (query: QueryOf<'listAdminLedgerTransactions'>) =>
    request<ResponseBody<'listAdminLedgerTransactions'>>(
      withQuery('/api/admin/ledger/transactions', query),
    ),
  transaction: (transactionID: string) =>
    request<ResponseBody<'getAdminLedgerTransaction'>>(
      `/api/admin/ledger/transactions/${id(transactionID)}`,
    ),
  repairCall: (callID: string, body: RequestBody<'repairAdminLedgerCall'>) =>
    post<'repairAdminLedgerCall'>(`/api/admin/ledger/repair-call/${id(callID)}`, body),
}

export type AdminQuery<Op extends keyof operations> = QueryOf<Op>
