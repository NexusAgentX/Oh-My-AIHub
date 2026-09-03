export type AccountStatus = 'active' | 'disabled'

export type Account = {
  id: string
  username: string
  display_name: string
  is_admin: boolean
  status: AccountStatus
  must_change_password: boolean
  version: number
  credit_limit: string
  credit_frozen: boolean
  posted_balance: string
  asset_reserved: string
  spend_authorized: string
  effective_credit_limit: string
  credit_used: string
  spendable_capacity: string
  over_limit: boolean
  created_at: string
  updated_at: string
  password_changed_at: string | null
}

export type WalletRiskStatus =
  | 'normal'
  | 'insufficient'
  | 'over_limit'
  | 'credit_frozen'

export type Wallet = {
  posted_balance: string
  asset_reserved: string
  spend_authorized: string
  credit_limit: string
  effective_credit_limit: string
  credit_used: string
  credit_frozen: boolean
  spendable_capacity: string
  over_limit: boolean
  risk_status: WalletRiskStatus
  updated_at: string
}

export type WalletRecoveryActionKind =
  | 'market'
  | 'create_buy_order'
  | 'my_orders'

export type WalletRecoveryAction = {
  kind: WalletRecoveryActionKind
  href: string
}

export type LedgerCounterparty = {
  account_kind: 'user' | 'platform_incentive' | 'platform_loss'
  account_id: string
  business_role: string
  amount: string
}

export type LedgerEntry = {
  id: string
  transaction_id: string
  entry_ordinal: number
  business_role: string
  amount: string
  posted_balance_before: string
  posted_balance_after: string
  created_at: string
  transaction_kind: string
  reason: string
  reference_type: string
  reference_id: string
  actor_account_id: string
  reversal_of_transaction_id: string
  hold_id: string
  counterparties: LedgerCounterparty[]
}

export type LedgerMetrics = {
  total_posted_balance: string
  positive_posted_balance: string
  negative_posted_balance: string
  posted_projection_difference: string
  posted_projection_mismatch_accounts: number
  asset_reservation_difference: string
  spend_authorization_difference: string
  hold_projection_mismatch_accounts: number
  zero_sum: boolean
  ledger_consistent: boolean
  total_credit_limit: string
  credit_capacity_used: string
  asset_reserved: string
  spend_authorized: string
  incentive_posted_balance: string
  loss_posted_balance: string
  over_limit_accounts: number
  credit_frozen_accounts: number
  ledger_account_count: number
}

export type CatalogModelStatus = 'active' | 'disabled'

export type CatalogModel = {
  id: string
  name: string
  provider: string
  context_window: number
  parameter_info: string
  input_modalities: string[]
  output_modalities: string[]
  supports_tools: boolean
  supports_structured_output: boolean
  supports_vision: boolean
  input_price: string
  output_price: string
  cache_write_price: string
  cache_read_price: string
  price_unit: 'points_per_million_tokens'
  status: CatalogModelStatus
  version: number
  created_at: string
  updated_at: string
  price_updated_at: string
}

export type CreatedCredential = {
  username: string
  initialPassword: string
}

export type ModelInput = Omit<
  CatalogModel,
  'price_unit' | 'version' | 'created_at' | 'updated_at' | 'price_updated_at'
>

export type ChannelStatus = 'draft' | 'published' | 'paused' | 'deleted'
export type ChannelOfferStatus = 'active' | 'disabled' | 'deleted'
export type ChannelProtocol =
  | 'openai_chat_completions'
  | 'openai_responses'
  | 'anthropic_messages'
  | 'google_gemini_generate_content'
export type ValidationStatus = 'in_progress' | 'passed' | 'failed'

export type ValidationSummary = {
  id: string
  validation_version: number
  attempt_seq: number
  status: ValidationStatus
  error_category: string
  http_status: number | null
  duration_milliseconds: number
  started_at: string
  completed_at: string | null
}

export type AuthorizedValidationAttempt = ValidationSummary & {
  actor_account_id: string
  raw_error: string
  raw_error_truncated: boolean
}

export type ChannelOffer = {
  id: string
  model_id: string
  model_name: string
  model_provider: string
  protocol: ChannelProtocol
  upstream_model_id?: string
  multiplier: string
  status: ChannelOfferStatus
  validation_version: number
  version?: number
  eligible: boolean
  ineligible_reason: string
  input_price?: string | null
  output_price?: string | null
  cache_write_price?: string | null
  cache_read_price?: string | null
  latest_validation: ValidationSummary | null
  created_at?: string
  updated_at?: string
}

export type Channel = {
  id: string
  owner_account_id: string
  owner_display_name: string
  display_name: string
  base_url?: string
  credential_configured: boolean
  credential_version: number
  credential_updated_at: string | null
  status: ChannelStatus
  version: number
  offers: ChannelOffer[]
  average_rating: string | null
  rating_count: number
  current_user_rating?: number | null
  created_at?: string
  updated_at?: string
}

export type MarketOffer = {
  offer_id: string
  channel_id: string
  channel_display_name: string
  owner_account_id: string
  owner_display_name: string
  model_id: string
  model_name: string
  model_provider: string
  protocol: ChannelProtocol
  multiplier: string
  input_price: string
  output_price: string
  cache_write_price: string
  cache_read_price: string
  validation_status: ValidationStatus
  last_tested_at: string | null
  average_rating: string | null
  rating_count: number
  call_success_rate: string | null
  ttft_milliseconds: number | null
  tokens_per_second: string | null
  call_count: number | null
}

export type MarketChannel = {
  id: string
  display_name: string
  owner_account_id: string
  owner_display_name: string
  status: 'published' | 'paused'
  offers: MarketOffer[]
  average_rating: string | null
  rating_count: number
  current_user_rating: number | null
}

export type AdminChannelOffer = {
  id: string
  model_id: string
  model_name: string
  model_provider: string
  protocol: ChannelProtocol
  multiplier: string
  status: ChannelOfferStatus
  validation_version: number
  latest_validation: ValidationSummary | null
}

export type AdminChannel = {
  id: string
  owner_account_id: string
  owner_display_name: string
  display_name: string
  credential_configured: boolean
  credential_version: number
  credential_updated_at: string | null
  status: ChannelStatus
  version: number
  offers: AdminChannelOffer[]
  average_rating: string | null
  rating_count: number
  created_at: string
  updated_at: string
}

export type ChannelOfferInput = {
  model_id: string
  protocol: ChannelProtocol
  upstream_model_id: string
  multiplier: string
}

export type C2CSide = 'sell' | 'buy'
export type C2COrderStatus = 'open' | 'allocated' | 'filled' | 'cancelled'
export type C2CTradeStatus =
  | 'awaiting_payment'
  | 'paid'
  | 'disputed'
  | 'released_to_buyer'
  | 'returned_to_seller'
  | 'cancelled'
  | 'expired'
export type C2CPaymentMethodType =
  | 'wechat'
  | 'alipay'
  | 'bank_transfer'
  | 'other'
export type C2CResolutionAction =
  | 'release_to_buyer'
  | 'return_to_seller'
  | 'extend_review'

export type C2CPaymentMethod = {
  id: string
  type: C2CPaymentMethodType
  position: number
  contact: string
  instructions: string
  qr_available: boolean
  qr_url: string
}

export type C2COrder = {
  id: string
  owner_account_id: string
  owner_display_name: string
  side: C2CSide
  unit_price_fen: number
  total: string
  available: string
  allocated: string
  settled: string
  closed: string
  minimum: string
  maximum: string
  status: C2COrderStatus
  payment_types: C2CPaymentMethodType[]
  payment_methods: C2CPaymentMethod[]
  created_at: string
  updated_at: string
  cancelled_at: string | null
}

export type C2CEvidence = {
  id: string
  uploader_account_id: string
  uploader_name: string
  kind: 'payment' | 'dispute'
  mime_type: 'image/jpeg' | 'image/png'
  size_bytes: number
  width: number
  height: number
  created_at: string
  deleted_at: string | null
  download_url: string
}

export type C2CStatement = {
  id: string
  actor_account_id: string
  actor_display_name: string
  text: string
  character_count: number
  created_at: string
  deleted_at: string | null
}

export type C2CEvent = {
  id: number
  actor_account_id: string
  action: string
  reason: string
  ledger_transaction_id: string
  created_at: string
}

export type C2CTrade = {
  id: string
  order_id: string
  order_side: C2CSide
  buyer_account_id: string
  buyer_display_name: string
  seller_account_id: string
  seller_display_name: string
  quantity: string
  unit_price_fen: number
  fiat_amount_fen: number
  status: C2CTradeStatus
  payment_method: C2CPaymentMethod | null
  payment_reference: string
  payment_reference_deleted_at: string | null
  payment_deadline: string
  review_due_at: string | null
  ledger_transaction_id: string
  evidence: C2CEvidence[]
  statements: C2CStatement[]
  events: C2CEvent[]
  created_at: string
  updated_at: string
  paid_at: string | null
  resolved_at: string | null
}

export type C2CMarketMetrics = {
  guidance_price_fen: number
  latest_price_fen: number | null
  best_bid_fen: number | null
  best_ask_fen: number | null
  spread_fen: number | null
}

export type C2CMarket = {
  metrics: C2CMarketMetrics
  sell_orders: C2COrder[]
  buy_orders: C2COrder[]
}
