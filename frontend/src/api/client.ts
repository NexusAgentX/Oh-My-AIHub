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
  LedgerEntry,
  LedgerMetrics,
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
  if (init?.body) headers.set('Content-Type', 'application/json')
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
}
