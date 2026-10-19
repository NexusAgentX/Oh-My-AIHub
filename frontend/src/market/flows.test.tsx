// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { createMemoryRouter, Outlet, RouterProvider } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { MarketGroupPage, MarketModelDetailPage, MarketModelsPage, MarketReportPage, MarketSupplierDetailPage } from './Pages'
import { MarketHallChannelPage, MarketHallPage } from './Hall'
import { UnsavedGuardProvider } from '../ui/UnsavedGuard'

let host: HTMLDivElement, root: Root, client: QueryClient, router: ReturnType<typeof createMemoryRouter>
let paths: string[], failMore: boolean, holdSearch: boolean, pendingDetail: boolean
const model = (id: string) => ({ id, model_types: ['text'], formats: ['openai_responses'], protocols: ['openai-responses'], online_channels: 1, price: null, offers: [] })
const channel = (id: string) => ({ id, name: `渠道 ${id}`, summary: '', description: '', supplier_id: 'supplier', supplier_name: '供应商', group_id: 'group', group_name: '分组', is_mine: true, models: ['model-a'], formats: ['openai_responses'] })
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  paths = []; failMore = false; holdSearch = false; pendingDetail = false
  vi.stubGlobal('fetch', vi.fn(async (path: string) => {
    paths.push(path)
    const url = new URL(path, 'http://localhost'), cursor = url.searchParams.get('cursor'), q = url.searchParams.get('q')
    const json = (data: unknown, status = 200) => new Response(JSON.stringify(data), { status })
    if (url.pathname === '/api/market/models') {
      if (q && holdSearch) return new Promise<Response>(() => {})
      if (cursor && failMore) return json({ message: '合成翻页失败' }, 503)
      return json({ items: [model(cursor ? 'model-b' : q || 'model-a')], total: 2, next_cursor: cursor ? null : 'snapshot-page-2', price_sort_fallback: false, suppliers: [], groups: [] })
    }
    if (url.pathname === '/api/market/channels') return json({ items: [channel(cursor ? 'b' : 'a')], total: 2, next_cursor: cursor ? null : 'page-2' })
    if (url.pathname === '/api/market/suppliers') return pendingDetail ? new Promise<Response>(() => {}) : json({ items: [], groups: [] })
    if (pendingDetail) return new Promise<Response>(() => {})
    return json({ message: '合成详情失败' }, 503)
  }))
})
afterEach(async () => { if (root) await act(async () => root.unmount()); client?.clear(); router?.dispose(); host?.remove(); vi.unstubAllGlobals(); vi.restoreAllMocks() })
async function mount(url: string) {
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  router = createMemoryRouter([{ element: <UnsavedGuardProvider><Outlet /></UnsavedGuardProvider>, children: [
    { path: '/models', element: <MarketModelsPage /> }, { path: '/models/:model', element: <MarketModelDetailPage /> },
    { path: '/channel-hall', element: <MarketHallPage /> }, { path: '/channel-hall/:id', element: <MarketHallChannelPage /> },
    { path: '/suppliers/:id', element: <MarketSupplierDetailPage /> }, { path: '/groups/:id', element: <MarketGroupPage /> }, { path: '/reports/:id', element: <MarketReportPage /> },
  ] }], { initialEntries: [url] })
  await act(async () => root.render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>))
}
async function wait(assertion: () => void) { const until = Date.now() + 2000; for (;;) { await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) }); try { assertion(); return } catch (error) { if (Date.now() > until) throw error } } }
async function click(text: string) { const target = [...host.querySelectorAll<HTMLButtonElement>('button')].find(x => x.textContent === text)!; expect(target, text).toBeDefined(); await act(async () => target.click()) }
function field(label: string) { return host.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`) ?? [...host.querySelectorAll('label')].find(x => x.querySelector('.field-label')?.textContent === label)?.querySelector<HTMLInputElement>('input') }
async function fill(label: string, value: string) { const input = field(label)!; expect(input, label).toBeDefined(); await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) }) }

it('appends pages without sharing snapshot cursors, and retains loaded rows when the next page fails', async () => {
  await mount('/models?cursor=old&limit=100')
  await wait(() => expect(host.textContent).toContain('model-a'))
  expect(router.state.location.search).toBe('')
  failMore = true; await click('加载更多')
  await wait(() => expect(host.textContent).toContain('合成翻页失败'))
  expect(host.textContent).toContain('model-a')
  failMore = false; await click('重试加载')
  await wait(() => expect(host.textContent).toContain('model-b'))
  expect(host.querySelectorAll('.model-card')).toHaveLength(2)
  expect(paths.some(path => path.includes('cursor=snapshot-page-2'))).toBe(true)
  expect(router.state.location.search).toBe('')
})
it('debounces search, replaces URL filters and leaves old rows visible during the request', async () => {
  await mount('/models')
  await wait(() => expect(host.textContent).toContain('model-a'))
  holdSearch = true
  await fill('搜索模型 ID', 'new-model')
  expect(router.state.location.search).toBe('')
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 350)) })
  expect(router.state.location.search).toBe('?q=new-model')
  expect(router.state.historyAction).toBe('REPLACE')
  expect(host.textContent).toContain('model-a')
  expect(host.textContent).toContain('正在更新')
  expect(host.querySelector('[aria-busy="true"]')).not.toBeNull()
})
it('commits numeric filters on Enter instead of each digit and omits false defaults', async () => {
  await mount('/models')
  await wait(() => expect(host.textContent).toContain('model-a'))
  await fill('最低实测上下文（token）', '1000')
  expect(router.state.location.search).toBe('')
  expect(host.textContent).toContain('待应用')
  await act(async () => field('最低实测上下文（token）')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
  await wait(() => expect(router.state.location.search).toBe('?min_context=1000'))
  await click('清空筛选')
  expect(router.state.location.search).toBe('')
})
it('appends hall rows and carries the filtered source to channel details', async () => {
  await mount('/channel-hall?group_id=group')
  await wait(() => expect(host.textContent).toContain('渠道 a'))
  expect(host.querySelector('a[href="/supply/channels/new?group=group"]')).not.toBeNull()
  await click('加载更多'); await wait(() => expect(host.textContent).toContain('渠道 b'))
  expect(host.querySelectorAll('.hall-channel-card')).toHaveLength(2)
  await act(async () => host.querySelector<HTMLAnchorElement>('h2 a[href="/channel-hall/a"]')!.click())
  expect(router.state.location.state?.from).toEqual({ to: '/channel-hall?group_id=group', label: '渠道大厅' })
})
it.each([
  ['/models/model-a', 'model-a'], ['/channel-hall/a', '渠道详情'], ['/suppliers/supplier', '供应商详情'], ['/groups/group', '分组详情'], ['/reports/report', '公开检测报告'],
])('retains the title while loading and after failure at %s', async (url, title) => {
  pendingDetail = true; await mount(url)
  expect(host.querySelector('h1')?.textContent).toBe(title)
  expect(host.textContent).toContain('正在加载')
  pendingDetail = false
  await act(async () => client.resetQueries())
  await wait(() => expect(host.textContent).not.toContain('正在加载'))
  expect(host.querySelector('h1')?.textContent).toBe(title)
})
