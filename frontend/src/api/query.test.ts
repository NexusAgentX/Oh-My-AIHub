import { describe, expect, it } from 'vitest'
import { ApiError } from './client'
import { createQueryClient, errorMessage, shouldRetry } from './query'

describe('errorMessage', () => {
  it('uses the backend message of an ApiError', () => {
    expect(errorMessage(new ApiError(403, 'forbidden', '没有权限'), '兜底')).toBe('没有权限')
  })

  it('falls back for unknown errors', () => {
    expect(errorMessage(new TypeError('Failed to fetch'), '加载失败')).toBe('加载失败')
    expect(errorMessage(undefined, '加载失败')).toBe('加载失败')
  })
})

describe('shouldRetry', () => {
  it('never retries deterministic 4xx failures', () => {
    expect(shouldRetry(0, new ApiError(404, 'not_found', 'x'))).toBe(false)
    expect(shouldRetry(0, new ApiError(401, 'authentication_required', 'x'))).toBe(false)
    expect(shouldRetry(0, new ApiError(501, 'not_implemented', 'x'))).toBe(false)
  })

  it('retries network and 5xx failures at most twice', () => {
    expect(shouldRetry(0, new TypeError('offline'))).toBe(true)
    expect(shouldRetry(1, new ApiError(503, 'unavailable', 'x'))).toBe(true)
    expect(shouldRetry(2, new ApiError(503, 'unavailable', 'x'))).toBe(false)
  })
})

describe('createQueryClient', () => {
  it('does not refetch on window focus and keeps mutations single-shot', () => {
    const options = createQueryClient().getDefaultOptions()
    expect(options.queries?.refetchOnWindowFocus).toBe(false)
    expect(options.mutations?.retry).toBe(false)
  })
})
