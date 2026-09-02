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
  balance: string
  frozen_balance: string
  available_credit: string
  created_at: string
  updated_at: string
  password_changed_at: string | null
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
