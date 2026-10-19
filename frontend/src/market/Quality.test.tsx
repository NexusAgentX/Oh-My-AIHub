// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'
import { loadMarketChannelNames } from './queries'
import { MarketQualitySection } from './Quality'

let root: Root | undefined, host: HTMLDivElement | undefined, client: QueryClient | undefined
afterEach(async () => {
  if (root) await act(async () => root!.unmount())
  client?.clear(); host?.remove(); root = undefined; host = undefined; client = undefined; vi.unstubAllGlobals()
})

it('loads every supplier channel page with the same filter and cancellation signal', async () => {
  const controller = new AbortController()
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    const query = new URL(path, 'http://example.test').searchParams
    expect(query.get('supplier_id')).toBe('supplier')
    expect(init?.signal).toBe(controller.signal)
    return new Response(JSON.stringify(query.get('cursor') ? { items: [{ id: 'b', name: 'B' }], next_cursor: null } : { items: [{ id: 'a', name: 'A' }], next_cursor: 'next-page' }))
  })
  vi.stubGlobal('fetch', fetcher)
  expect(await loadMarketChannelNames('supplier', controller.signal)).toEqual([{ id: 'a', name: 'A' }, { id: 'b', name: 'B' }])
  expect(fetcher).toHaveBeenCalledTimes(2)
})

it('rejects a repeating directory cursor instead of truncating or looping', async () => {
  const fetcher = vi.fn(async () => new Response(JSON.stringify({ items: [], next_cursor: 'same' })))
  vi.stubGlobal('fetch', fetcher)
  await expect(loadMarketChannelNames('supplier')).rejects.toThrow('分页已变化')
  expect(fetcher).toHaveBeenCalledTimes(2)
})

it('loads quality on first opening and retains the chosen range after collapsing', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const fetcher = vi.fn((_path: string) => new Promise<Response>(() => {}))
  vi.stubGlobal('fetch', fetcher)
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: 0 } } })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  await act(async () => root!.render(<QueryClientProvider client={client!}><MemoryRouter><MarketQualitySection kind="channel" id="channel" /></MemoryRouter></QueryClientProvider>))
  expect(fetcher).not.toHaveBeenCalled()
  const toggle = host.querySelector<HTMLButtonElement>('.disclosure-toggle')!
  await act(async () => { toggle.click(); await new Promise(resolve => setTimeout(resolve, 0)) })
  expect(fetcher).toHaveBeenCalledTimes(1)
  expect(String(fetcher.mock.calls[0]?.[0])).toContain('/api/channel-hall/channel/analysis?')
  const range = host.querySelector<HTMLSelectElement>('select')!
  expect(range.value).toBe('7')
  await act(async () => { range.value = '30'; range.dispatchEvent(new Event('change', { bubbles: true })); await new Promise(resolve => setTimeout(resolve, 0)) })
  expect(fetcher).toHaveBeenCalledTimes(2)
  await act(async () => { toggle.click(); toggle.click(); await new Promise(resolve => setTimeout(resolve, 0)) })
  expect(toggle.getAttribute('aria-expanded')).toBe('true')
  expect(host.querySelector('select')).toBe(range)
  expect(range.value).toBe('30')
  expect(fetcher).toHaveBeenCalledTimes(2)
})
