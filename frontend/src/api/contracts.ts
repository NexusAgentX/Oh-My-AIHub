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
  call_success_rate?: string | null
  ttft_milliseconds?: number | null
  tokens_per_second?: string | null
  call_count?: number | null
  provider_income?: string | null
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

export type APIKeyStatus = 'active' | 'disabled' | 'deleted'

export type APIKeyPoolMember = {
  priority: number
  offer_id: string
  channel_id: string
  channel_name: string
  provider_name: string
  added_validation_version: number
  current_validation_version: number
  eligible: boolean
  ineligible_reason: string
  input_price: string
  output_price: string
  cache_write_price: string
  cache_read_price: string
  success_rate: string | null
  ttft_milliseconds: number | null
  tokens_per_second: string | null
}

export type APIKeyPool = {
  id: string
  model_id: string
  model_name: string
  protocol: ChannelProtocol
  version: number
  members: APIKeyPoolMember[]
  created_at: string
  updated_at: string
}

export type APIKey = {
  id: string
  display_name: string
  prefix: string
  generation: number
  status: APIKeyStatus
  version: number
  pools: APIKeyPool[]
  last_used_at: string | null
  created_at: string
  updated_at: string
}

export type APIKeyPoolInput = {
  model_id: string
  protocol: ChannelProtocol
  offer_ids: string[]
}

export type GatewayUsage = {
  input_tokens: number
  output_tokens: number
  cache_write_tokens: number
  cache_read_tokens: number
}

export type GatewayAttemptStatus =
  | 'in_progress'
  | 'pending_delivery'
  | 'succeeded'
  | 'failed'
  | 'cancelled'
  | 'incomplete'

export type GatewayAttempt = {
  id: string
  sequence: number
  offer_id: string
  channel_name: string
  provider_account_id: string
  status: GatewayAttemptStatus
  http_status: number
  error_code: string
  raw_error: string
  raw_error_truncated: boolean
  semantic_committed: boolean
  ttft_milliseconds: number | null
  duration_milliseconds: number | null
  usage: GatewayUsage | null
  tokens_per_second: string | null
  started_at: string
  completed_at: string | null
}

export type GatewayCallStatus =
  | 'rejected'
  | 'in_progress'
  | 'pending_delivery'
  | 'succeeded'
  | 'failed'
  | 'incomplete'
  | 'cancelled'

export type GatewayCall = {
  id: string
  consumer_account_id: string
  api_key_id: string
  key_prefix: string
  key_generation: number
  pool_id: string
  pool_version: number
  model_id: string
  protocol: ChannelProtocol
  status: GatewayCallStatus
  decision_code: string
  candidate_count: number
  attempt_count: number
  hold_id: string
  preauthorized: string
  zero_hold_reason: string
  fee_rate_version: number
  fee_rate_nano: number
  final_offer_id: string
  final_channel_name: string
  completion_reason: string
  usage: GatewayUsage | null
  provider_charge: string
  platform_fee: string
  final_http_status: number
  attempts: GatewayAttempt[]
  created_at: string
  completed_at: string | null
}

export type GatewayDashboard = {
  consumer_spent: string
  provider_income: string
  today_spent: string
  today_succeeded_calls: number
  today_external_provider_income: string
  active_key_count: number
  pool_count: number
  healthy_offer_count: number
  unhealthy_offer_count: number
  pending_items: number
  recent_calls: GatewayCall[]
}

export type ProviderIncomeRow = {
  account_id: string
  display_name: string
  total_income: string
  other_consumer_income: string
  own_usage_income: string
  success_rate: string | null
}

export type ProviderIncomeSnapshot = {
  from: string
  to: string
  total_income: string
  other_consumer_income: string
  own_usage_income: string
  active_providers: number
  providers: ProviderIncomeRow[]
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

export type OpsNegativeBalanceRisk = {
  account_id: string
  username: string
  posted_balance: string
  negative_since: string
  last_financial_activity: string
  inactive_days: number
  over_limit: boolean
  credit_limit: string
}

export type OpsAPIMetrics = {
  precheck_rejected: number
  reached_upstream: number
  succeeded: number
  all_failed: number
  incomplete_after_commit: number
  cancelled: number
  terminal_reached: number
  success_rate: string | null
  attempt_count: number
  attempt_succeeded: number
  average_ttft_milliseconds: number | null
  average_tokens_per_second: string | null
}

export type OpsConsumptionMetrics = {
  consumer_spend: string
  provider_income: string
  own_usage_income: string
  other_consumer_income: string
  platform_fee: string
}

export type OpsC2COrderStatusCount = { side: string; status: string; count: number }
export type OpsC2CTradeStatusCount = { status: string; count: number }

export type OpsC2CMetrics = {
  orders: OpsC2COrderStatusCount[]
  trades: OpsC2CTradeStatusCount[]
  quote: {
    last_traded_price_fen: number | null
    best_bid_price_fen: number | null
    best_ask_price_fen: number | null
    spread_fen: number | null
  }
}

export type OpsConcentrationMetrics = {
  positive_user_count: number
  total_positive: string
  top1_share: string | null
  top5_share: string | null
  hhi: string | null
}

export type OpsMetrics = {
  from: string
  to: string
  ledger: LedgerMetrics
  effective_credit: string
  negative_balances: OpsNegativeBalanceRisk[]
  api: OpsAPIMetrics
  consumption: OpsConsumptionMetrics
  c2c: OpsC2CMetrics
  concentration: OpsConcentrationMetrics
}

export type OpsAnomaly = {
  kind: string
  attention: boolean
  count: number
  detail: string
  drilldown: string
}

export type OpsAnomalies = {
  hard_anomalies: OpsAnomaly[]
  attention_items: OpsAnomaly[]
  hard_count: number
  checked_at: string
}

export type OpsInspection = {
  id: string
  inspection_version: string
  triggered_by: string
  zero_sum_ok: boolean
  projection_ok: boolean
  call_settlement_ok: boolean
  c2c_consistency_ok: boolean
  zero_sum_difference: string
  posted_projection_difference: string
  asset_projection_difference: string
  authorization_projection_difference: string
  successful_calls_without_settlement: number
  settlements_without_ledger_transaction: number
  c2c_quantity_violations: number
  c2c_hold_violations: number
  checked_at: string
}

export type OpsTrialSummary = {
  generated_at: string
  non_admin_accounts: number
  published_channels: number
  passed_offers: number
  active_api_keys: number
  calls_succeeded: number
  calls_failed: number
  calls_incomplete: number
  first_call_at: string | null
  last_terminal_call_at: string | null
  c2c_open_orders: number
  c2c_released_trades: number
  c2c_disputed_open: number
  ledger_zero_sum_ok: boolean
  last_inspection_ok: boolean | null
  last_inspection_at: string | null
  inspection_pass_count: number
  inspection_total_count: number
}
