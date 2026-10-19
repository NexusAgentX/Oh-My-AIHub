// @vitest-environment jsdom
import { act, useEffect } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import type { Points } from '../api/types'
import { BalanceCard } from './BalanceCard'
import { useC2CTrade, useMyC2CTrades } from './queries'
import { useCountdown } from './TradeParts'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => Promise<void>)[] = []
afterEach(async () => { for (const clean of cleanups.splice(0)) await clean(); vi.restoreAllMocks(); vi.useRealTimers() })
async function mount(element: React.ReactNode) {
  const host = document.createElement('div'), root = createRoot(host)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } })
  await act(async () => root.render(<QueryClientProvider client={client}>{element}</QueryClientProvider>))
  cleanups.push(async () => { await act(async () => root.unmount()); client.clear() })
  return host
}
async function tick(ms = 1) { await act(async () => { await vi.advanceTimersByTimeAsync(ms) }) }

it.each(['compact', 'full'] as const)('shows balance + credit as available in the %s card', variant => {
  const summary = { balance: '50', credit_limit: '100', available: '150', trend: [] } as unknown as Points
  const html = renderToStaticMarkup(<MemoryRouter><BalanceCard summary={summary} variant={variant} /></MemoryRouter>)
  expect(html).toMatch(/可用额度 <span class="num">150<\/span>/)
  expect(html).toMatch(/信用额度 <span class="num">100<\/span>/)
  expect(html).not.toContain('还能透支')
})

it('requests pending trades on every cursor page, including records beyond the former 100 limit', async () => {
  vi.useFakeTimers()
  const urls: URL[] = []
  vi.spyOn(globalThis, 'fetch').mockImplementation(async input => {
    const url = new URL(String(input), 'http://localhost'); urls.push(url)
    const page = Number(url.searchParams.get('cursor') || 0)
    return new Response(JSON.stringify({ items: Array.from({ length: 50 }, (_, i) => ({ id: String(page * 50 + i) })), next_cursor: page < 2 ? String(page + 1) : null }), { status: 200 })
  })
  let query!: ReturnType<typeof useMyC2CTrades>
  function Probe() { const result = useMyC2CTrades({ pending: true }); useEffect(() => { query = result }); return <p>{result.data?.items.length}</p> }
  const host = await mount(<Probe />); await tick()
  for (let i = 0; i < 2; i++) { await act(async () => { await query.fetchNextPage() }); await tick() }
  expect(host.textContent).toBe('150')
  expect(query.hasNextPage).toBe(false)
  expect(urls.map(url => url.searchParams.get('cursor'))).toEqual([null, '1', '2'])
  expect(urls.every(url => url.searchParams.get('pending') === 'true' && url.searchParams.get('limit') === '50')).toBe(true)
})

it('polls unfinished trades and stops immediately when the response becomes terminal', async () => {
  vi.useFakeTimers()
  let status = 'paid'
  const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify({ trade: { id: 'trade', status } }), { status: 200 }))
  function Probe() { const query = useC2CTrade('trade'); return <p>{query.data?.status}</p> }
  const host = await mount(<Probe />); await tick()
  expect(fetch).toHaveBeenCalledTimes(1)
  status = 'released'; await tick(15_001)
  expect(host.textContent).toBe('released')
  expect(fetch).toHaveBeenCalledTimes(2)
  await tick(60_000)
  expect(fetch).toHaveBeenCalledTimes(2)
})

it('pauses the countdown while hidden and catches up on return, stopping at expiry', async () => {
  vi.useFakeTimers(); vi.setSystemTime(new Date('2026-10-19T10:00:00Z'))
  function Probe() { return <p>{useCountdown('2026-10-19T10:00:05Z')}</p> }
  const host = await mount(<Probe />)
  expect(host.textContent).toBe('5')
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
  await act(async () => { document.dispatchEvent(new Event('visibilitychange')) })
  await tick(10_000); expect(host.textContent).toBe('5')
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  await act(async () => { document.dispatchEvent(new Event('visibilitychange')) })
  expect(host.textContent).toBe('0')
  expect(vi.getTimerCount()).toBe(0)
})
