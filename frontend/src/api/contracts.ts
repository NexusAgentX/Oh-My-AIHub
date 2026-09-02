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
