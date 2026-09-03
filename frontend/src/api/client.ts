import type {
  Account,
  AccountStatus,
  AdminChannel,
  APIKey,
  APIKeyPoolInput,
  AuthorizedValidationAttempt,
  CatalogModel,
  Channel,
  ChannelOffer,
  ChannelOfferInput,
  ChannelProtocol,
  GatewayCall,
  GatewayDashboard,
  ProviderIncomeSnapshot,
  C2CMarket,
  C2COrder,
  C2CPaymentMethodType,
  C2CResolutionAction,
  C2CSide,
  C2CTrade,
  LedgerEntry,
  LedgerMetrics,
  OpsMetrics,
  OpsAnomalies,
  OpsInspection,
  OpsTrialSummary,
  MarketChannel,
  MarketOffer,
  ModelInput,
  Wallet,
  WalletRecoveryAction,
} from './contracts'

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

export function marketOffersPath(input: {
  modelID?: string
  protocol?: ChannelProtocol | ''
  owner?: string
  sort?:
    | 'input_price'
    | 'output_price'
    | 'cache_write_price'
    | 'cache_read_price'
    | 'rating'
    | 'success_rate'
    | 'ttft'
    | 'tps'
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
    return (await request<{ account: Account }>('/api/auth/session')).account
  },
  async instanceState() {
    return request<{ initialized: boolean }>('/api/instance')
  },
  async initializeInstance(username: string, displayName: string, password: string) {
    return request<{ initialized: boolean; account: Account }>('/api/instance/initialize', {
      method: 'POST',
      body: JSON.stringify({ username, display_name: displayName, password }),
    })
  },
  async login(username: string, password: string) {
    return (
      await request<{ account: Account }>('/api/auth/login', {
        method: 'POST',
        body: JSON.stringify({ username, password }),
      })
    ).account
  },
  logout() {
    return request<void>('/api/auth/logout', { method: 'POST' })
  },
  async changePassword(currentPassword: string, newPassword: string) {
    return (
      await request<{ account: Account }>('/api/account/password', {
        method: 'PUT',
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      })
    ).account
  },
  async account() {
    return (await request<{ account: Account }>('/api/account')).account
  },
  async accounts(query = '') {
    const suffix = query ? `?q=${encodeURIComponent(query)}` : ''
    return (
      await request<{ accounts: Account[] }>(`/api/admin/accounts${suffix}`)
    ).accounts
  },
  async createAccount(input: {
    username: string
    display_name: string
    credit_limit: string
    is_admin: boolean
    status: AccountStatus
  }) {
    return request<{ account: Account; initial_password: string }>(
      '/api/admin/accounts',
      { method: 'POST', body: JSON.stringify(input) },
    )
  },
  async updateAccount(
    accountID: string,
    expectedVersion: number,
    input: {
      status?: AccountStatus
      credit_limit?: string
      credit_frozen?: boolean
      is_admin?: boolean
    },
  ) {
    return (
      await request<{ account: Account }>(
        `/api/admin/accounts/${encodeURIComponent(accountID)}`,
        {
          method: 'PATCH',
          body: JSON.stringify({ ...input, expected_version: expectedVersion }),
        },
      )
    ).account
  },
  wallet() {
    return request<{ wallet: Wallet; recovery_actions: WalletRecoveryAction[] }>(
      '/api/wallet',
    )
  },
  walletEntries(before = '', limit = 20) {
    return request<{ entries: LedgerEntry[]; next_before: string }>(
      ledgerEntriesPath('/api/wallet/entries', before, limit),
    )
  },
  async ledgerMetrics() {
    return (
      await request<{ metrics: LedgerMetrics }>('/api/admin/ledger/metrics')
    ).metrics
  },
  async opsMetrics(from: string, to: string) {
    const query = `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`
    return (await request<{ metrics: OpsMetrics }>(`/api/admin/ops/metrics${query}`)).metrics
  },
  async opsProviderIncome(from: string, to: string) {
    const query = `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`
    return (
      await request<{ provider_income: ProviderIncomeSnapshot }>(
        `/api/admin/ops/providers${query}`,
      )
    ).provider_income
  },
  async opsAnomalies() {
    return (await request<{ anomalies: OpsAnomalies }>('/api/admin/ops/anomalies')).anomalies
  },
  async opsInspections(limit = 20) {
    return (await request<{ inspections: OpsInspection[] }>(`/api/admin/ops/inspections?limit=${limit}`)).inspections
  },
  async runOpsInspection() {
    return (await request<{ inspection: OpsInspection }>('/api/admin/ops/inspections', { method: 'POST' })).inspection
  },
  async opsTrialSummary() {
    return (await request<{ trial_summary: OpsTrialSummary }>('/api/admin/ops/trial-summary')).trial_summary
  },
  async adminAccountWallet(accountID: string) {
    return (
      await request<{ wallet: Wallet }>(
        `/api/admin/ledger/accounts/${encodeURIComponent(accountID)}/wallet`,
      )
    ).wallet
  },
  adminAccountEntries(accountID: string, before = '', limit = 20) {
    return request<{ entries: LedgerEntry[]; next_before: string }>(
      ledgerEntriesPath(
        `/api/admin/ledger/accounts/${encodeURIComponent(accountID)}/entries`,
        before,
        limit,
      ),
    )
  },
  async adminSystemWallet(systemKind: 'platform_incentive' | 'platform_loss') {
    return (
      await request<{ wallet: Wallet }>(
        `/api/admin/ledger/system-accounts/${systemKind}/wallet`,
      )
    ).wallet
  },
  adminSystemEntries(
    systemKind: 'platform_incentive' | 'platform_loss',
    before = '',
    limit = 20,
  ) {
    return request<{ entries: LedgerEntry[]; next_before: string }>(
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
      await request<{ models: CatalogModel[] }>(`${prefix}${suffix}`)
    ).models
  },
  async model(modelID: string, admin = false) {
    const prefix = admin ? '/api/admin/models' : '/api/models'
    return (
      await request<{ model: CatalogModel }>(`${prefix}/${modelID}`)
    ).model
  },
  async createModel(input: ModelInput) {
    return (
      await request<{ model: CatalogModel }>('/api/admin/models', {
        method: 'POST',
        body: JSON.stringify(input),
      })
    ).model
  },
  async updateModel(
    modelID: string,
    expectedVersion: number,
    input: Omit<ModelInput, 'id'>,
  ) {
    return (
      await request<{ model: CatalogModel }>(`/api/admin/models/${modelID}`, {
        method: 'PUT',
        body: JSON.stringify({ ...input, expected_version: expectedVersion }),
      })
    ).model
  },
  async channels() {
    return (await request<{ channels: Channel[] }>('/api/channels')).channels
  },
  async channel(channelID: string) {
    return (
      await request<{ channel: Channel }>(
        `/api/channels/${encodeURIComponent(channelID)}`,
      )
    ).channel
  },
  async createChannel(input: {
    display_name: string
    base_url: string
    credential: string
    offers: ChannelOfferInput[]
  }) {
    return (
      await request<{ channel: Channel }>('/api/channels', {
        method: 'POST',
        body: JSON.stringify(input),
      })
    ).channel
  },
  async updateChannel(
    channelID: string,
    input: {
      display_name: string
      base_url: string
      credential?: string
      expected_version: number
    },
  ) {
    return (
      await request<{ channel: Channel }>(
        `/api/channels/${encodeURIComponent(channelID)}`,
        { method: 'PATCH', body: JSON.stringify(input) },
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
      await request<{ channel: Channel }>(
        `/api/channels/${encodeURIComponent(channelID)}/${action}`,
        {
          method: 'POST',
          body: JSON.stringify({ expected_version: expectedVersion, reason }),
        },
      )
    ).channel
  },
  async deleteChannel(channelID: string, expectedVersion: number, reason = '') {
    return (
      await request<{ channel: Channel }>(
        `/api/channels/${encodeURIComponent(channelID)}`,
        {
          method: 'DELETE',
          body: JSON.stringify({ expected_version: expectedVersion, reason }),
        },
      )
    ).channel
  },
  async revokeChannelCredential(channelID: string, expectedVersion: number) {
    return (
      await request<{ channel: Channel }>(
        `/api/channels/${encodeURIComponent(channelID)}/credential-revoke`,
        {
          method: 'POST',
          body: JSON.stringify({ expected_version: expectedVersion }),
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
      await request<{ offer: ChannelOffer }>(
        `/api/channels/${encodeURIComponent(channelID)}/offers`,
        {
          method: 'POST',
          body: JSON.stringify({
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
      await request<{ offer: ChannelOffer }>(
        `/api/channel-offers/${encodeURIComponent(offerID)}`,
        {
          method: 'PATCH',
          body: JSON.stringify({
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
      await request<{ offer: ChannelOffer }>(
        `/api/channel-offers/${encodeURIComponent(offerID)}/${action}`,
        {
          method: 'POST',
          body: JSON.stringify({ expected_version: expectedVersion }),
        },
      )
    ).offer
  },
  async deleteChannelOffer(offerID: string, expectedVersion: number) {
    return (
      await request<{ offer: ChannelOffer }>(
        `/api/channel-offers/${encodeURIComponent(offerID)}`,
        {
          method: 'DELETE',
          body: JSON.stringify({ expected_version: expectedVersion }),
        },
      )
    ).offer
  },
  async validateChannelOffer(offerID: string, admin = false) {
    const prefix = admin ? '/api/admin/channel-offers' : '/api/channel-offers'
    return (
      await request<{ validation: AuthorizedValidationAttempt }>(
        `${prefix}/${encodeURIComponent(offerID)}/validation-attempts`,
        {
          method: 'POST',
          body: JSON.stringify({ confirmed_upstream_cost: true }),
        },
      )
    ).validation
  },
  async channelValidationAttempts(offerID: string, admin = false, limit = 50) {
    const prefix = admin ? '/api/admin/channel-offers' : '/api/channel-offers'
    return (
      await request<{ validation_attempts: AuthorizedValidationAttempt[] }>(
        `${prefix}/${encodeURIComponent(offerID)}/validation-attempts?limit=${limit}`,
      )
    ).validation_attempts
  },
  marketOffers(input: Parameters<typeof marketOffersPath>[0] = {}) {
    return request<{ offers: MarketOffer[]; next_after: string }>(
      marketOffersPath(input),
    )
  },
  async marketChannel(channelID: string) {
    return (
      await request<{ channel: MarketChannel }>(
        `/api/market/channels/${encodeURIComponent(channelID)}`,
      )
    ).channel
  },
  async rateChannel(channelID: string, score: number) {
    return (
      await request<{ channel: MarketChannel }>(
        `/api/market/channels/${encodeURIComponent(channelID)}/rating`,
        { method: 'PUT', body: JSON.stringify({ score }) },
      )
    ).channel
  },
  async apiKeys() {
    return (await request<{ keys: APIKey[] }>('/api/keys')).keys
  },
  async apiKey(keyID: string) {
    return (
      await request<{ key: APIKey }>(
        `/api/keys/${encodeURIComponent(keyID)}`,
      )
    ).key
  },
  createAPIKey(displayName: string, pools: APIKeyPoolInput[]) {
    return request<{ key: APIKey; secret: string }>('/api/keys', {
      method: 'POST',
      body: JSON.stringify({ display_name: displayName, pools }),
    })
  },
  async updateAPIKey(
    keyID: string,
    expectedVersion: number,
    displayName: string,
    pools: APIKeyPoolInput[],
  ) {
    return (
      await request<{ key: APIKey }>(
        `/api/keys/${encodeURIComponent(keyID)}`,
        {
          method: 'PATCH',
          body: JSON.stringify({
            display_name: displayName,
            pools,
            expected_version: expectedVersion,
          }),
        },
      )
    ).key
  },
  rotateAPIKey(keyID: string, expectedVersion: number) {
    return request<{ key: APIKey; secret: string }>(
      `/api/keys/${encodeURIComponent(keyID)}/rotate`,
      {
        method: 'POST',
        body: JSON.stringify({ expected_version: expectedVersion }),
      },
    )
  },
  async setAPIKeyStatus(
    keyID: string,
    action: 'disable' | 'enable',
    expectedVersion: number,
  ) {
    return (
      await request<{ key: APIKey }>(
        `/api/keys/${encodeURIComponent(keyID)}/${action}`,
        {
          method: 'POST',
          body: JSON.stringify({ expected_version: expectedVersion }),
        },
      )
    ).key
  },
  async deleteAPIKey(keyID: string, expectedVersion: number) {
    return (
      await request<{ key: APIKey }>(
        `/api/keys/${encodeURIComponent(keyID)}`,
        {
          method: 'DELETE',
          body: JSON.stringify({ expected_version: expectedVersion }),
        },
      )
    ).key
  },
  async addAPIKeyPoolMember(
    keyID: string,
    expectedVersion: number,
    input: {
      model_id: string
      protocol: ChannelProtocol
      offer_id: string
      priority: number
    },
  ) {
    return (
      await request<{ key: APIKey }>(
        `/api/keys/${encodeURIComponent(keyID)}/pool-members`,
        {
          method: 'POST',
          body: JSON.stringify({ ...input, expected_version: expectedVersion }),
        },
      )
    ).key
  },
  async gatewayCalls(limit = 50) {
    return (
      await request<{ calls: GatewayCall[] }>(`/api/calls?limit=${limit}`)
    ).calls
  },
  async gatewayCall(callID: string) {
    return (
      await request<{ call: GatewayCall }>(
        `/api/calls/${encodeURIComponent(callID)}`,
      )
    ).call
  },
  gatewayDashboard() {
    return request<GatewayDashboard>('/api/dashboard')
  },
  async adminChannels() {
    return (await request<{ channels: AdminChannel[] }>('/api/admin/channels')).channels
  },
  async adminChannel(channelID: string) {
    return (
      await request<{ channel: AdminChannel }>(
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
      await request<{ channel: AdminChannel }>(path, {
        method: action === 'delete' ? 'DELETE' : 'POST',
        body: JSON.stringify({ expected_version: expectedVersion, reason }),
      })
    ).channel
  },
  c2cMarket() {
    return request<C2CMarket>('/api/c2c/market')
  },
  async c2cOrder(orderID: string) {
    return (
      await request<{ order: C2COrder }>(
        `/api/c2c/orders/${encodeURIComponent(orderID)}`,
      )
    ).order
  },
  async createC2COrder(input: {
    side: C2CSide
    unit_price_fen: number
    total: string
    minimum: string
    maximum: string
    payment_methods: Array<{
      type: C2CPaymentMethodType
      contact: string
      instructions: string
      qr?: File | null
    }>
  }) {
    const key = crypto.randomUUID()
    const hasFiles = input.payment_methods.some((method) => method.qr)
    const payload = {
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
      form.set('payload', JSON.stringify(payload))
      input.payment_methods.forEach((method, index) => {
        if (method.qr) form.set(`payment_qr_${index}`, method.qr)
      })
      body = form
    } else {
      body = JSON.stringify(payload)
    }
    return (
      await request<{ order: C2COrder }>('/api/c2c/orders', {
        method: 'POST',
        headers: { 'Idempotency-Key': key },
        body,
      })
    ).order
  },
  async takeC2COrder(orderID: string, quantity: string, paymentMethodID: string) {
    return (
      await request<{ trade: C2CTrade }>(
        `/api/c2c/orders/${encodeURIComponent(orderID)}/take`,
        {
          method: 'POST',
          headers: { 'Idempotency-Key': crypto.randomUUID() },
          body: JSON.stringify({ quantity, payment_method_id: paymentMethodID }),
        },
      )
    ).trade
  },
  async cancelC2COrder(orderID: string) {
    return (
      await request<{ order: C2COrder }>(
        `/api/c2c/orders/${encodeURIComponent(orderID)}/cancel`,
        { method: 'POST', headers: { 'Idempotency-Key': crypto.randomUUID() } },
      )
    ).order
  },
  async c2cActivity() {
    return request<{ orders: C2COrder[]; trades: C2CTrade[] }>('/api/c2c/me')
  },
  async c2cTrade(tradeID: string) {
    return (
      await request<{ trade: C2CTrade }>(
        `/api/c2c/trades/${encodeURIComponent(tradeID)}`,
      )
    ).trade
  },
  async markC2CPaid(tradeID: string, paymentReference: string, screenshot?: File | null) {
    const payload = { payment_reference: paymentReference }
    let body: BodyInit
    if (screenshot) {
      const form = new FormData()
      form.set('payload', JSON.stringify(payload))
      form.set('screenshot', screenshot)
      body = form
    } else {
      body = JSON.stringify(payload)
    }
    return (
      await request<{ trade: C2CTrade }>(
        `/api/c2c/trades/${encodeURIComponent(tradeID)}/paid`,
        {
          method: 'POST',
          headers: { 'Idempotency-Key': crypto.randomUUID() },
          body,
        },
      )
    ).trade
  },
  async cancelC2CTrade(tradeID: string) {
    return (
      await request<{ trade: C2CTrade }>(
        `/api/c2c/trades/${encodeURIComponent(tradeID)}/cancel`,
        { method: 'POST', headers: { 'Idempotency-Key': crypto.randomUUID() } },
      )
    ).trade
  },
  async releaseC2CTrade(tradeID: string) {
    return (
      await request<{ trade: C2CTrade }>(
        `/api/c2c/trades/${encodeURIComponent(tradeID)}/release`,
        { method: 'POST', headers: { 'Idempotency-Key': crypto.randomUUID() } },
      )
    ).trade
  },
  async submitC2CDispute(
    tradeID: string,
    statement: string,
    evidence: File[],
    append = false,
  ) {
    const payload = { statement }
    let body: BodyInit
    if (evidence.length > 0) {
      const form = new FormData()
      form.set('payload', JSON.stringify(payload))
      evidence.forEach((file) => form.append('evidence', file))
      body = form
    } else {
      body = JSON.stringify(payload)
    }
    return (
      await request<{ trade: C2CTrade }>(
        `/api/c2c/trades/${encodeURIComponent(tradeID)}/${append ? 'evidence' : 'dispute'}`,
        {
          method: 'POST',
          headers: { 'Idempotency-Key': crypto.randomUUID() },
          body,
        },
      )
    ).trade
  },
  async adminC2CDisputes() {
    return (
      await request<{ trades: C2CTrade[] }>('/api/admin/c2c/disputes')
    ).trades
  },
  async adminCancelC2COrder(orderID: string, reason: string) {
    return (
      await request<{ order: C2COrder }>(
        `/api/admin/c2c/orders/${encodeURIComponent(orderID)}/cancel`,
        {
          method: 'POST',
          headers: { 'Idempotency-Key': crypto.randomUUID() },
          body: JSON.stringify({ reason }),
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
      await request<{ trade: C2CTrade }>(
        `/api/admin/c2c/trades/${encodeURIComponent(tradeID)}/resolve`,
        {
          method: 'POST',
          headers: { 'Idempotency-Key': crypto.randomUUID() },
          body: JSON.stringify({ action, reason }),
        },
      )
    ).trade
  },
}
