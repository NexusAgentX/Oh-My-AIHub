import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../api/client'

afterEach(() => vi.unstubAllGlobals())

describe('C2C client contracts', () => {
  it('sends JSON orders with an idempotency key and no file fields', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ order: {} }), {
      status: 201, headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)

    await api.createC2COrder({
      side: 'sell', unit_price_fen: 100, total: '10', minimum: '1', maximum: '10',
      payment_methods: [{ type: 'wechat', contact: 'wx', instructions: '' }],
    })

    const [path, init] = fetchMock.mock.calls[0]
    const headers = new Headers(init?.headers)
    expect(path).toBe('/api/c2c/orders')
    expect(headers.get('Content-Type')).toBe('application/json')
    expect(headers.get('Idempotency-Key')).toMatch(/^[0-9a-f-]{36}$/)
    expect(JSON.parse(String(init?.body)).payment_methods).toEqual([{ type: 'wechat', contact: 'wx', instructions: '' }])
  })

  it('sends JSON take commands with an idempotency key', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ trade: {} }), {
      status: 201, headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)

    await api.takeC2COrder('order/one', '2.5', 'payment-method')

    const [path, init] = fetchMock.mock.calls[0]
    const headers = new Headers(init?.headers)
    expect(path).toBe('/api/c2c/orders/order%2Fone/take')
    expect(headers.get('Content-Type')).toBe('application/json')
    expect(headers.get('Idempotency-Key')).toMatch(/^[0-9a-f-]{36}$/)
    expect(JSON.parse(String(init?.body))).toEqual({ quantity: '2.5', payment_method_id: 'payment-method' })
  })

  it('sends text-only payment declarations and dispute statements as JSON', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ trade: {} }), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)

    await api.markC2CPaid('trade', 'ref-1')
    await api.submitC2CDispute('trade', 'opening statement')
    await api.submitC2CDispute('trade', 'follow-up', true)

    const calls = fetchMock.mock.calls.map(([path, init]) => [path, JSON.parse(String(init?.body)), new Headers(init?.headers)] as const)
    expect(calls.map(([path]) => path)).toEqual([
      '/api/c2c/trades/trade/paid', '/api/c2c/trades/trade/dispute', '/api/c2c/trades/trade/statements',
    ])
    expect(calls.map(([, body]) => body)).toEqual([
      { payment_reference: 'ref-1' }, { statement: 'opening statement' }, { statement: 'follow-up' },
    ])
    for (const [, , headers] of calls) {
      expect(headers.get('Content-Type')).toBe('application/json')
      expect(headers.get('Idempotency-Key')).toMatch(/^[0-9a-f-]{36}$/)
    }
  })
})
