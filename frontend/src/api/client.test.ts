import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  api,
  ApiError,
  changesAuthenticatedAccount,
  ledgerEntriesPath,
  marketOffersPath,
} from './client'

afterEach(() => vi.unstubAllGlobals())

describe('changesAuthenticatedAccount', () => {
  it.each([
    [401, 'authentication_required'],
    [403, 'password_change_required'],
    [403, 'administrator_required'],
  ])('notifies for %s %s', (status, code) => {
    expect(
      changesAuthenticatedAccount(new ApiError(status, code, 'message')),
    ).toBe(true)
  })

  it('leaves a valid session intact after an incorrect current password', () => {
    expect(
      changesAuthenticatedAccount(
        new ApiError(401, 'invalid_credentials', '当前密码不正确'),
      ),
    ).toBe(false)
  })
})

describe('channel client contracts', () => {
  it('encodes market filters and cursor deterministically', () => {
    expect(marketOffersPath({
      modelID: 'openai/gpt 5',
      protocol: 'openai_responses',
      owner: '共享 者',
      sort: 'rating',
      after: 'offer/id',
      limit: 12,
    })).toBe('/api/market/offers?limit=12&model_id=openai%2Fgpt+5&protocol=openai_responses&owner=%E5%85%B1%E4%BA%AB+%E8%80%85&sort=rating&after=offer%2Fid')
  })

  it('sends channel CAS when adding an offer', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ offer: {} }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await api.addChannelOffer('channel/one', 9, {
      model_id: 'openai/gpt-5',
      protocol: 'openai_responses',
      upstream_model_id: 'gpt-5',
      multiplier: '1.25',
    })
    const [path, init] = fetchMock.mock.calls[0]
    expect(path).toBe('/api/channels/channel%2Fone/offers')
    expect(JSON.parse(String(init?.body))).toEqual({
      model_id: 'openai/gpt-5', protocol: 'openai_responses', upstream_model_id: 'gpt-5', multiplier: '1.25', expected_version: 9,
    })
  })

  it('requires explicit upstream-cost confirmation for validation', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ validation: {} }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await api.validateChannelOffer('offer/one')
    const [, init] = fetchMock.mock.calls[0]
    expect(JSON.parse(String(init?.body))).toEqual({ confirmed_upstream_cost: true })
  })
})

describe('ledger client contracts', () => {
  it('builds stable cursor pagination without losing precision', () => {
    expect(ledgerEntriesPath('/api/wallet/entries', '9007199254740993', 50)).toBe(
      '/api/wallet/entries?limit=50&before=9007199254740993',
    )
  })

  it('sends the version precondition with a credit freeze update', async () => {
    const fetchMock = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(
        JSON.stringify({ account: {} }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    await api.updateAccount('account/with space', 7, { credit_frozen: true })

    expect(fetchMock).toHaveBeenCalledOnce()
    const [path, init] = fetchMock.mock.calls[0]
    expect(path).toBe('/api/admin/accounts/account%2Fwith%20space')
    expect(JSON.parse(String(init?.body))).toEqual({
      credit_frozen: true,
      expected_version: 7,
    })
  })
})
