import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../api/client'

afterEach(() => vi.unstubAllGlobals())

describe('C2C client contracts', () => {
  it('uses multipart boundaries from the browser and carries idempotency for image orders', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ order: {} }), {
      status: 201, headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)
    const image = new File([new Uint8Array([1, 2, 3])], 'qr.png', { type: 'image/png' })

    await api.createC2COrder({
      unit_price_fen: 100, total: '10', minimum: '1', maximum: '10',
      payment_methods: [{ type: 'wechat', contact: '', instructions: '', qr: image }],
    })

    const [path, init] = fetchMock.mock.calls[0]
    const headers = new Headers(init?.headers)
    expect(path).toBe('/api/c2c/orders')
    expect(init?.body).toBeInstanceOf(FormData)
    expect(headers.has('Content-Type')).toBe(false)
    expect(headers.get('Idempotency-Key')).toMatch(/^[0-9a-f-]{36}$/)
    const form = init?.body as FormData
    expect(JSON.parse(String(form.get('payload'))).payment_methods[0].qr_field).toBe('payment_qr_0')
    expect(form.get('payment_qr_0')).toBe(image)
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
