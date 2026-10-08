import type { RequestBody, ResponseBody } from './types'
import type { operations } from './schema.gen'

/** 后端统一错误结构：{"error": "<code>", "message": "<中文>"}。 */
type ErrorPayload = {
  error?: string
  message?: string
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

/** 序列化 JSON 请求体，并按 operationId 对照规范校验其结构。 */
function jsonBody<Op extends keyof operations>(body: RequestBody<Op>) {
  return JSON.stringify(body)
}

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
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
      typeof payload.error === 'string' ? payload.error : 'request_failed',
      payload.message ?? '请求失败，请稍后重试',
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
  async me() {
    return (await request<ResponseBody<'getMe'>>('/api/me')).account
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
      await request<ResponseBody<'changePassword'>>('/api/me/password', {
        method: 'POST',
        body: jsonBody<'changePassword'>({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      })
    ).account
  },
  points() {
    return request<ResponseBody<'getPoints'>>('/api/points')
  },
}
