import type {
  Account,
  AccountStatus,
  CatalogModel,
  ModelInput,
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
}
