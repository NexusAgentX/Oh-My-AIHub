import type {
  APIKeyPoolInput,
  C2CPaymentMethodRequest,
  C2CResolutionAction,
  ChannelOfferInput,
  ChannelProtocol,
  ModelInput,
  RequestBody,
  ResponseBody,
} from './types'
import type { operations } from './schema.gen'

type ErrorPayload = {
  error?: {
    code?: string
    message?: string
  }
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

let authFailureHandler: ((error: ApiError) => void) | null = null

export function setAuthFailureHandler(
  handler: ((error: ApiError) => void) | null,
) {
  authFailureHandler = handler
}

export function changesAuthenticatedAccount(error: ApiError) {
  return (
    error.code === 'authentication_required' ||
    error.code === 'password_change_required' ||
    error.code === 'administrator_required'
  )
}

export function ledgerEntriesPath(
  path: string,
  before = '',
  limit = 20,
) {
  const query = new URLSearchParams({ limit: String(limit) })
  if (before) query.set('before', before)
  return `${path}?${query.toString()}`
}

type MarketOfferSort = NonNullable<
  NonNullable<operations['listMarketOffers']['parameters']['query']>['sort']
>

export function marketOffersPath(input: {
  modelID?: string
  protocol?: ChannelProtocol | ''
  owner?: string
  sort?: MarketOfferSort
  after?: string
  limit?: number
} = {}) {
  const query = new URLSearchParams({ limit: String(input.limit ?? 20) })
  if (input.modelID) query.set('model_id', input.modelID)
  if (input.protocol) query.set('protocol', input.protocol)
  if (input.owner) query.set('owner', input.owner)
  if (input.sort) query.set('sort', input.sort)
  if (input.after) query.set('after', input.after)
  return `/api/market/offers?${query.toString()}`
}

/** 序列化 JSON 请求体，并按 operationId 对照规范校验其结构。 */
function jsonBody<Op extends keyof operations>(body: RequestBody<Op>) {
  return JSON.stringify(body)
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  if (init?.body && !(init.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json')
  }
  const response = await fetch(path, {
    ...init,
    headers,
    credentials: 'same-origin',
  })
  if (!response.ok) {
    let payload: ErrorPayload = {}
    try {
      payload = (await response.json()) as ErrorPayload
    } catch {
      // The public error remains intentionally generic when a proxy fails.
    }
    const error = new ApiError(
      response.status,
      payload.error?.code ?? 'request_failed',
      payload.error?.message ?? '请求失败，请稍后重试',
    )
    if (changesAuthenticatedAccount(error)) {
      authFailureHandler?.(error)
    }
    throw error
  }
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}

export const api = {
  async session() {
    return (await request<ResponseBody<'getSession'>>('/api/auth/session')).account
  },
  async instanceState() {
    return request<ResponseBody<'getInstance'>>('/api/instance')
  },
  async initializeInstance(username: string, displayName: string, password: string) {
    return request<ResponseBody<'initializeInstance'>>('/api/instance/initialize', {
      method: 'POST',
      body: jsonBody<'initializeInstance'>({ username, display_name: displayName, password }),
    })
  },
  async login(username: string, password: string) {
    return (
      await request<ResponseBody<'login'>>('/api/auth/login', {
        method: 'POST',
        body: jsonBody<'login'>({ username, password }),
      })
    ).account
  },
  logout() {
    return request<void>('/api/auth/logout', { method: 'POST' })
  },
  async changePassword(currentPassword: string, newPassword: string) {
    return (
      await request<ResponseBody<'changePassword'>>('/api/account/password', {
        method: 'PUT',
        body: jsonBody<'changePassword'>({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      })
    ).account
  },
  async account() {
    return (await request<ResponseBody<'getAccount'>>('/api/account')).account
  },
  async accounts(query = '') {
    const suffix = query ? `?q=${encodeURIComponent(query)}` : ''
    return (
      await request<ResponseBody<'listAccounts'>>(`/api/admin/accounts${suffix}`)
    ).accounts
  },
  async createAccount(input: RequestBody<'createAccount'>) {
    return request<ResponseBody<'createAccount'>>(
      '/api/admin/accounts',
      { method: 'POST', body: jsonBody<'createAccount'>(input) },
    )
  },
  async updateAccount(
    accountID: string,
    expectedVersion: number,
    input: Omit<RequestBody<'updateAccount'>, 'expected_version'>,
  ) {
    return (
      await request<ResponseBody<'updateAccount'>>(
        `/api/admin/accounts/${encodeURIComponent(accountID)}`,
        {
          method: 'PATCH',
          body: jsonBody<'updateAccount'>({ ...input, expected_version: expectedVersion }),
        },
      )
    ).account
  },
  async resetAccountPassword(accountID: string) {
    return request<ResponseBody<'resetAccountPassword'>>(
      `/api/admin/accounts/${encodeURIComponent(accountID)}/password-reset`,
      { method: 'POST', body: jsonBody<'resetAccountPassword'>({}) },
    )
  },
  wallet() {
    return request<ResponseBody<'getWallet'>>(
      '/api/wallet',
    )
  },
  walletEntries(before = '', limit = 20) {
    return request<ResponseBody<'listWalletEntries'>>(
      ledgerEntriesPath('/api/wallet/entries', before, limit),
    )
  },
  async ledgerMetrics() {
    return (
      await request<ResponseBody<'getLedgerMetrics'>>('/api/admin/ledger/metrics')
    ).metrics
  },
  async opsMetrics(from: string, to: string) {
    const query = `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`
    return (await request<ResponseBody<'getOpsMetrics'>>(`/api/admin/ops/metrics${query}`)).metrics
  },
  async opsProviderIncome(from: string, to: string) {
    const query = `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`
    return (
      await request<ResponseBody<'getOpsProviderIncome'>>(
        `/api/admin/ops/providers${query}`,
      )
    ).provider_income
  },
  async opsAnomalies() {
    return (await request<ResponseBody<'getOpsAnomalies'>>('/api/admin/ops/anomalies')).anomalies
  },
  async opsInspections(limit = 20) {
    return (await request<ResponseBody<'listOpsInspections'>>(`/api/admin/ops/inspections?limit=${limit}`)).inspections
  },
  async runOpsInspection() {
    return (await request<ResponseBody<'runOpsInspection'>>('/api/admin/ops/inspections', { method: 'POST' })).inspection
  },
  adminFeeRates(limit = 10) {
    return request<ResponseBody<'getFeeRates'>>(`/api/admin/fee-rate?limit=${limit}`)
  },
  async setAdminFeeRate(expectedVersion: number, feeRate: string, reason: string) {
    return (
      await request<ResponseBody<'setFeeRate'>>('/api/admin/fee-rate', {
        method: 'PUT',
        body: jsonBody<'setFeeRate'>({ expected_version: expectedVersion, fee_rate: feeRate, reason }),
      })
    ).fee_rate
  },
  async adminAccountWallet(accountID: string) {
    return (
      await request<ResponseBody<'getAdminLedgerAccountWallet'>>(
        `/api/admin/ledger/accounts/${encodeURIComponent(accountID)}/wallet`,
      )
    ).wallet
  },
  adminAccountEntries(accountID: string, before = '', limit = 20) {
    return request<ResponseBody<'listAdminLedgerAccountEntries'>>(
      ledgerEntriesPath(
        `/api/admin/ledger/accounts/${encodeURIComponent(accountID)}/entries`,
        before,
        limit,
      ),
    )
  },
  async adminSystemWallet(systemKind: 'platform_incentive' | 'platform_loss') {
    return (
      await request<ResponseBody<'getAdminLedgerSystemWallet'>>(
        `/api/admin/ledger/system-accounts/${systemKind}/wallet`,
      )
    ).wallet
  },
  adminSystemEntries(
    systemKind: 'platform_incentive' | 'platform_loss',
    before = '',
    limit = 20,
  ) {
    return request<ResponseBody<'listAdminLedgerSystemEntries'>>(
      ledgerEntriesPath(
        `/api/admin/ledger/system-accounts/${systemKind}/entries`,
        before,
        limit,
      ),
    )
  },
  async models(query = '', admin = false) {
    const suffix = query ? `?q=${encodeURIComponent(query)}` : ''
    const prefix = admin ? '/api/admin/models' : '/api/models'
    return (
      await request<ResponseBody<'listModels'>>(`${prefix}${suffix}`)
    ).models
  },
  async model(modelID: string, admin = false) {
    const prefix = admin ? '/api/admin/models' : '/api/models'
    return (
      await request<ResponseBody<'getModel'>>(`${prefix}/${modelID}`)
    ).model
  },
  async createModel(input: ModelInput) {
    return (
      await request<ResponseBody<'createModel'>>('/api/admin/models', {
        method: 'POST',
        body: jsonBody<'createModel'>(input),
      })
    ).model
  },
  async updateModel(
    modelID: string,
    expectedVersion: number,
    input: Omit<ModelInput, 'id'>,
  ) {
    return (
      await request<ResponseBody<'updateModel'>>(`/api/admin/models/${modelID}`, {
        method: 'PUT',
        body: jsonBody<'updateModel'>({ ...input, expected_version: expectedVersion }),
      })
    ).model
  },
  async channels() {
    return (await request<ResponseBody<'listChannels'>>('/api/channels')).channels
  },
  async channel(channelID: string) {
    return (
      await request<ResponseBody<'getChannel'>>(
        `/api/channels/${encodeURIComponent(channelID)}`,
      )
    ).channel
  },
  async createChannel(input: RequestBody<'createChannel'>) {
    return (
      await request<ResponseBody<'createChannel'>>('/api/channels', {
        method: 'POST',
        body: jsonBody<'createChannel'>(input),
      })
    ).channel
  },
  async updateChannel(
    channelID: string,
    input: RequestBody<'updateChannel'>,
  ) {
    return (
      await request<ResponseBody<'updateChannel'>>(
        `/api/channels/${encodeURIComponent(channelID)}`,
        { method: 'PATCH', body: jsonBody<'updateChannel'>(input) },
      )
    ).channel
  },
  async setChannelStatus(
    channelID: string,
    action: 'publish' | 'pause',
    expectedVersion: number,
    reason = '',
  ) {
    return (
      await request<ResponseBody<'publishChannel'>>(
        `/api/channels/${encodeURIComponent(channelID)}/${action}`,
        {
          method: 'POST',
          body: jsonBody<'publishChannel'>({ expected_version: expectedVersion, reason }),
        },
      )
    ).channel
  },
  async deleteChannel(channelID: string, expectedVersion: number, reason = '') {
    return (
      await request<ResponseBody<'deleteChannel'>>(
        `/api/channels/${encodeURIComponent(channelID)}`,
        {
          method: 'DELETE',
          body: jsonBody<'deleteChannel'>({ expected_version: expectedVersion, reason }),
        },
      )
    ).channel
  },
  async revokeChannelCredential(channelID: string, expectedVersion: number) {
    return (
      await request<ResponseBody<'revokeChannelCredential'>>(
        `/api/channels/${encodeURIComponent(channelID)}/credential-revoke`,
        {
          method: 'POST',
          body: jsonBody<'revokeChannelCredential'>({ expected_version: expectedVersion }),
        },
      )
    ).channel
  },
  async addChannelOffer(
    channelID: string,
    expectedChannelVersion: number,
    input: ChannelOfferInput,
  ) {
    return (
      await request<ResponseBody<'addChannelOffer'>>(
        `/api/channels/${encodeURIComponent(channelID)}/offers`,
        {
          method: 'POST',
          body: jsonBody<'addChannelOffer'>({
            ...input,
            expected_version: expectedChannelVersion,
          }),
        },
      )
    ).offer
  },
  async updateChannelOffer(
    offerID: string,
    expectedVersion: number,
    upstreamModelID: string,
    multiplier: string,
  ) {
    return (
      await request<ResponseBody<'updateChannelOffer'>>(
        `/api/channel-offers/${encodeURIComponent(offerID)}`,
        {
          method: 'PATCH',
          body: jsonBody<'updateChannelOffer'>({
            expected_version: expectedVersion,
            upstream_model_id: upstreamModelID,
            multiplier,
          }),
        },
      )
    ).offer
  },
  async setChannelOfferStatus(
    offerID: string,
    action: 'disable' | 'resume',
    expectedVersion: number,
  ) {
    return (
      await request<ResponseBody<'disableChannelOffer'>>(
        `/api/channel-offers/${encodeURIComponent(offerID)}/${action}`,
        {
          method: 'POST',
          body: jsonBody<'disableChannelOffer'>({ expected_version: expectedVersion }),
        },
      )
    ).offer
  },
  async deleteChannelOffer(offerID: string, expectedVersion: number) {
    return (
      await request<ResponseBody<'deleteChannelOffer'>>(
        `/api/channel-offers/${encodeURIComponent(offerID)}`,
        {
          method: 'DELETE',
          body: jsonBody<'deleteChannelOffer'>({ expected_version: expectedVersion }),
        },
      )
    ).offer
  },
  async validateChannelOffer(offerID: string, admin = false) {
    const prefix = admin ? '/api/admin/channel-offers' : '/api/channel-offers'
    return (
      await request<ResponseBody<'validateChannelOffer'>>(
        `${prefix}/${encodeURIComponent(offerID)}/validation-attempts`,
        {
          method: 'POST',
          body: jsonBody<'validateChannelOffer'>({ confirmed_upstream_cost: true }),
        },
      )
    ).validation
  },
  async channelValidationAttempts(offerID: string, admin = false, limit = 50) {
    const prefix = admin ? '/api/admin/channel-offers' : '/api/channel-offers'
    return (
      await request<ResponseBody<'listOfferValidationAttempts'>>(
        `${prefix}/${encodeURIComponent(offerID)}/validation-attempts?limit=${limit}`,
      )
    ).validation_attempts
  },
  marketOffers(input: Parameters<typeof marketOffersPath>[0] = {}) {
    return request<ResponseBody<'listMarketOffers'>>(
      marketOffersPath(input),
    )
  },
  async marketChannel(channelID: string) {
    return (
      await request<ResponseBody<'getMarketChannel'>>(
        `/api/market/channels/${encodeURIComponent(channelID)}`,
      )
    ).channel
  },
  async pendingItems() {
    return (await request<ResponseBody<'listPendingItems'>>('/api/dashboard/pending-items')).items
  },
  async apiKeys() {
    return (await request<ResponseBody<'listAPIKeys'>>('/api/keys')).keys
  },
  async apiKey(keyID: string) {
    return (
      await request<ResponseBody<'getAPIKey'>>(
        `/api/keys/${encodeURIComponent(keyID)}`,
      )
    ).key
  },
  createAPIKey(displayName: string, pools: APIKeyPoolInput[]) {
    return request<ResponseBody<'createAPIKey'>>('/api/keys', {
      method: 'POST',
      body: jsonBody<'createAPIKey'>({ display_name: displayName, pools }),
    })
  },
  async updateAPIKey(
    keyID: string,
    expectedVersion: number,
    displayName: string,
    pools: APIKeyPoolInput[],
  ) {
    return (
      await request<ResponseBody<'updateAPIKey'>>(
        `/api/keys/${encodeURIComponent(keyID)}`,
        {
          method: 'PATCH',
          body: jsonBody<'updateAPIKey'>({
            display_name: displayName,
            pools,
            expected_version: expectedVersion,
          }),
        },
      )
    ).key
  },
  rotateAPIKey(keyID: string, expectedVersion: number) {
    return request<ResponseBody<'rotateAPIKey'>>(
      `/api/keys/${encodeURIComponent(keyID)}/rotate`,
      {
        method: 'POST',
        body: jsonBody<'rotateAPIKey'>({ expected_version: expectedVersion }),
      },
    )
  },
  async setAPIKeyStatus(
    keyID: string,
    action: 'disable' | 'enable',
    expectedVersion: number,
  ) {
    return (
      await request<ResponseBody<'disableAPIKey'>>(
        `/api/keys/${encodeURIComponent(keyID)}/${action}`,
        {
          method: 'POST',
          body: jsonBody<'disableAPIKey'>({ expected_version: expectedVersion }),
        },
      )
    ).key
  },
  async deleteAPIKey(keyID: string, expectedVersion: number) {
    return (
      await request<ResponseBody<'deleteAPIKey'>>(
        `/api/keys/${encodeURIComponent(keyID)}`,
        {
          method: 'DELETE',
          body: jsonBody<'deleteAPIKey'>({ expected_version: expectedVersion }),
        },
      )
    ).key
  },
  async addAPIKeyPoolMember(
    keyID: string,
    expectedVersion: number,
    input: Omit<RequestBody<'addAPIKeyPoolMember'>, 'expected_version'>,
  ) {
    return (
      await request<ResponseBody<'addAPIKeyPoolMember'>>(
        `/api/keys/${encodeURIComponent(keyID)}/pool-members`,
        {
          method: 'POST',
          body: jsonBody<'addAPIKeyPoolMember'>({ ...input, expected_version: expectedVersion }),
        },
      )
    ).key
  },
  async gatewayCalls(limit = 50) {
    return (
      await request<ResponseBody<'listGatewayCalls'>>(`/api/calls?limit=${limit}`)
    ).calls
  },
  async gatewayCall(callID: string) {
    return (
      await request<ResponseBody<'getGatewayCall'>>(
        `/api/calls/${encodeURIComponent(callID)}`,
      )
    ).call
  },
  gatewayDashboard() {
    return request<ResponseBody<'getDashboard'>>('/api/dashboard')
  },
  async adminChannels() {
    return (await request<ResponseBody<'listAdminChannels'>>('/api/admin/channels')).channels
  },
  async adminChannel(channelID: string) {
    return (
      await request<ResponseBody<'getAdminChannel'>>(
        `/api/admin/channels/${encodeURIComponent(channelID)}`,
      )
    ).channel
  },
  async adminSetChannelStatus(
    channelID: string,
    action: 'pause' | 'delete',
    expectedVersion: number,
    reason: string,
  ) {
    const path = `/api/admin/channels/${encodeURIComponent(channelID)}${action === 'pause' ? '/pause' : ''}`
    return (
      await request<ResponseBody<'adminPauseChannel'>>(path, {
        method: action === 'delete' ? 'DELETE' : 'POST',
        body: jsonBody<'adminPauseChannel'>({ expected_version: expectedVersion, reason }),
      })
    ).channel
  },
  c2cMarket() {
    return request<ResponseBody<'getC2CMarket'>>('/api/c2c/market')
  },
  async c2cOrder(orderID: string) {
    return (
      await request<ResponseBody<'getC2COrder'>>(
        `/api/c2c/orders/${encodeURIComponent(orderID)}`,
      )
    ).order
  },
  async createC2COrder(
    input: Omit<RequestBody<'createC2COrder'>, 'payment_methods'> & {
      payment_methods: Array<
        Omit<C2CPaymentMethodRequest, 'qr_field'> & { qr?: File | null }
      >
    },
  ) {
    const key = crypto.randomUUID()
    const hasFiles = input.payment_methods.some((method) => method.qr)
    const payload: RequestBody<'createC2COrder'> = {
      ...input,
      payment_methods: input.payment_methods.map((method, index) => ({
        type: method.type,
        contact: method.contact,
        instructions: method.instructions,
        qr_field: method.qr ? `payment_qr_${index}` : '',
      })),
    }
    let body: BodyInit
    if (hasFiles) {
      const form = new FormData()
      form.set('payload', jsonBody<'createC2COrder'>(payload))
      input.payment_methods.forEach((method, index) => {
        if (method.qr) form.set(`payment_qr_${index}`, method.qr)
      })
      body = form
    } else {
      body = jsonBody<'createC2COrder'>(payload)
    }
    return (
      await request<ResponseBody<'createC2COrder'>>('/api/c2c/orders', {
        method: 'POST',
        headers: { 'Idempotency-Key': key },
        body,
      })
    ).order
  },
  async takeC2COrder(orderID: string, quantity: string, paymentMethodID: string) {
    return (
      await request<ResponseBody<'takeC2COrder'>>(
        `/api/c2c/orders/${encodeURIComponent(orderID)}/take`,
        {
          method: 'POST',
          headers: { 'Idempotency-Key': crypto.randomUUID() },
          body: jsonBody<'takeC2COrder'>({ quantity, payment_method_id: paymentMethodID }),
        },
      )
    ).trade
  },
  async cancelC2COrder(orderID: string) {
    return (
      await request<ResponseBody<'cancelC2COrder'>>(
        `/api/c2c/orders/${encodeURIComponent(orderID)}/cancel`,
        { method: 'POST', headers: { 'Idempotency-Key': crypto.randomUUID() } },
      )
    ).order
  },
  async c2cActivity() {
    return request<ResponseBody<'getC2CMyActivity'>>('/api/c2c/me')
  },
  async c2cTrade(tradeID: string) {
    return (
      await request<ResponseBody<'getC2CTrade'>>(
        `/api/c2c/trades/${encodeURIComponent(tradeID)}`,
      )
    ).trade
  },
  async markC2CPaid(tradeID: string, paymentReference: string) {
    return (
      await request<ResponseBody<'markC2CTradePaid'>>(
        `/api/c2c/trades/${encodeURIComponent(tradeID)}/paid`,
        {
          method: 'POST',
          headers: { 'Idempotency-Key': crypto.randomUUID() },
          body: jsonBody<'markC2CTradePaid'>({ payment_reference: paymentReference }),
        },
      )
    ).trade
  },
  async cancelC2CTrade(tradeID: string) {
    return (
      await request<ResponseBody<'cancelC2CTrade'>>(
        `/api/c2c/trades/${encodeURIComponent(tradeID)}/cancel`,
        { method: 'POST', headers: { 'Idempotency-Key': crypto.randomUUID() } },
      )
    ).trade
  },
  async releaseC2CTrade(tradeID: string) {
    return (
      await request<ResponseBody<'confirmC2CReceipt'>>(
        `/api/c2c/trades/${encodeURIComponent(tradeID)}/release`,
        { method: 'POST', headers: { 'Idempotency-Key': crypto.randomUUID() } },
      )
    ).trade
  },
  async submitC2CDispute(
    tradeID: string,
    statement: string,
    append = false,
  ) {
    return (
      await request<ResponseBody<'openC2CDispute'>>(
        `/api/c2c/trades/${encodeURIComponent(tradeID)}/${append ? 'statements' : 'dispute'}`,
        {
          method: 'POST',
          headers: { 'Idempotency-Key': crypto.randomUUID() },
          body: jsonBody<'openC2CDispute'>({ statement }),
        },
      )
    ).trade
  },
  async adminC2CDisputes() {
    return (
      await request<ResponseBody<'listC2CDisputes'>>('/api/admin/c2c/disputes')
    ).trades
  },
  async adminCancelC2COrder(orderID: string, reason: string) {
    return (
      await request<ResponseBody<'adminCancelC2COrder'>>(
        `/api/admin/c2c/orders/${encodeURIComponent(orderID)}/cancel`,
        {
          method: 'POST',
          headers: { 'Idempotency-Key': crypto.randomUUID() },
          body: jsonBody<'adminCancelC2COrder'>({ reason }),
        },
      )
    ).order
  },
  async resolveC2CDispute(
    tradeID: string,
    action: C2CResolutionAction,
    reason: string,
  ) {
    return (
      await request<ResponseBody<'resolveC2CDispute'>>(
        `/api/admin/c2c/trades/${encodeURIComponent(tradeID)}/resolve`,
        {
          method: 'POST',
          headers: { 'Idempotency-Key': crypto.randomUUID() },
          body: jsonBody<'resolveC2CDispute'>({ action, reason }),
        },
      )
    ).trade
  },
}
