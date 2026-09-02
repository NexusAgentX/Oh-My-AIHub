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
