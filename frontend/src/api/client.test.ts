import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError, apiGet, apiSend, changesAuthenticatedAccount, withQuery } from './client'

afterEach(() => vi.unstubAllGlobals())

describe('changesAuthenticatedAccount', () => {
  it.each([
    [401, 'authentication_required'],
    [403, 'password_change_required'],
    [403, 'administrator_required'],
  ])('notifies for %s %s', (status, code) => {
    expect(changesAuthenticatedAccount(new ApiError(status, code, 'message'))).toBe(true)
  })

  it('leaves a valid session intact after an incorrect current password', () => {
    expect(
      changesAuthenticatedAccount(new ApiError(401, 'invalid_credentials', '当前密码不正确')),
    ).toBe(false)
  })
})

describe('typed helpers', () => {
  it('builds query strings without empty values', () => {
    expect(withQuery('/api/calls', { model: 'gpt-5', outcome: '', cursor: undefined, limit: 50 })).toBe('/api/calls?model=gpt-5&limit=50')
    expect(withQuery('/api/calls', {})).toBe('/api/calls')
  })

  it('sends the Idempotency-Key header for ledger writes', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(
      JSON.stringify({ trade: { id: 't' } }),
      { status: 201, headers: { 'Content-Type': 'application/json' } },
    ))
    vi.stubGlobal('fetch', fetchMock)
    await apiSend<'createC2CTrade'>('POST', '/api/c2c/orders/o/trades', { amount: '1' }, { idempotencyKey: 'abc' })
    const [, init] = fetchMock.mock.calls[0]
    expect(new Headers(init?.headers).get('Idempotency-Key')).toBe('abc')
    expect(new Headers(init?.headers).get('Content-Type')).toBe('application/json')
    expect(init?.body).toBe('{"amount":"1"}')
  })
})

describe('request errors', () => {
  it('reads the flat error shape', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(
      JSON.stringify({ error: 'not_implemented', message: '该功能尚未实现' }),
      { status: 501, headers: { 'Content-Type': 'application/json' } },
    )))
    await expect(apiGet<'getHome'>('/api/home')).rejects.toMatchObject({ status: 501, code: 'not_implemented', message: '该功能尚未实现' })
  })

  it('falls back to a generic message when the body is not JSON', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('bad gateway', { status: 502 })))
    await expect(apiGet<'getPoints'>('/api/points')).rejects.toMatchObject({ status: 502, code: 'request_failed' })
  })

  it('changes passwords through POST /api/me/password', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(
      JSON.stringify({ account: { id: 'a' } }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    ))
    vi.stubGlobal('fetch', fetchMock)
    await api.changePassword('old-password', 'new-password-2026')
    const [path, init] = fetchMock.mock.calls[0]
    expect(path).toBe('/api/me/password')
    expect(init?.method).toBe('POST')
    expect(JSON.parse(String(init?.body))).toEqual({ current_password: 'old-password', new_password: 'new-password-2026' })
  })
})
