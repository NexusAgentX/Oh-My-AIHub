// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { createMemoryRouter, Outlet, RouterProvider } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { InboxProvider } from './InboxProvider'
import { InboxPage } from './InboxPage'
import { HomeActions, InboxBell } from './Components'
import { inboxFixture } from './fixtures.testdata'
import { homeActions, reminderCount } from './model'
import { createQueryClient } from '../api/query'
import { DomainStatusBadge, statusMeta } from '../ui/status'
vi.mock('../auth/AuthProvider', () => ({ useAuth: () => ({ account: { id: 'viewer', is_admin: true } }) }))
let host: HTMLDivElement, root: Root, client: QueryClient, router: ReturnType<typeof createMemoryRouter>
let fixture = inboxFixture(), fail = false, fetcher: ReturnType<typeof vi.fn>
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  fixture = inboxFixture(); fail = false
  fetcher = vi.fn(async () => new Response(JSON.stringify(fail ? { message: '待办服务暂不可用' } : fixture), { status: fail ? 503 : 200 }))
  vi.stubGlobal('fetch', fetcher)
})
afterEach(async () => { if (root) await act(async () => root.unmount()); client?.clear(); router?.dispose(); host?.remove(); vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })
async function mount(path = '/inbox') {
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  router = createMemoryRouter([{ element: <InboxProvider><InboxBell /><Outlet /></InboxProvider>, children: [
    { path: '/inbox', element: <InboxPage /> }, { path: '/home', element: <HomeActions /> }, { path: '/supply/channels/:id/config', element: <p>渠道配置</p> },
  ] }], { initialEntries: [path] })
  await act(async () => root.render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>))
}
async function wait(assertion: () => void) { const deadline = Date.now() + 2000; for (;;) { await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) }); try { assertion(); return } catch (error) { if (Date.now() > deadline) throw error } } }
async function click(label: string) { const button = [...host.querySelectorAll<HTMLButtonElement>('button')].find(x => x.textContent === label)!; expect(button, label).toBeDefined(); await act(async () => button.click()) }

it('uses complete server counts rather than the displayed rows and keeps incomplete counts unknown', () => {
  const source = fixture.sources[0]; source.counts!.action = 101
  expect(reminderCount([source])).toBe(101)
  source.state = 'limited'; source.counts = null
  expect(reminderCount([source])).toBeUndefined()
})
it('puts only deadline actions and blockers on the home preview', () => {
  const titles = homeActions(fixture.sources).map(item => item.title)
  expect(titles).toContain('积分交易 · 示例待付款'); expect(titles).toContain('示例共享渠道')
  expect(titles).not.toContain('示例供应商修订'); expect(titles).not.toContain('示例应用 Key'); expect(titles).not.toContain('demo-chat 能力检测')
})
it('preserves URL filters in object links and in browser Back', async () => {
  await mount('/inbox?category=anomaly&source=channels')
  await wait(() => expect(host.textContent).toContain('没有可用地址'))
  expect(host.querySelectorAll('.inbox-list li')).toHaveLength(1)
  await act(async () => host.querySelector<HTMLAnchorElement>('.inbox-list a')!.click())
  expect(router.state.location.state?.from).toEqual({ to: '/inbox?category=anomaly&source=channels', label: '待办' })
  await act(async () => router.navigate(-1))
  expect(router.state.location.search).toBe('?category=anomaly&source=channels')
})
it('renders failed-source uncertainty, known rows and source recovery links', async () => {
  fixture.sources[1].state = 'unavailable'; fixture.sources[1].counts = null
  await mount('/inbox?category=anomaly')
  await wait(() => expect(host.textContent).toContain('数量未知'))
  expect(host.textContent).toContain('没有可用地址')
  expect(host.querySelector('.inbox-bell')?.getAttribute('aria-label')).toContain('数量未完整读取')
  expect(host.querySelector('.inbox-refresh-state a[href="/supply/channels"]')).not.toBeNull()
  expect(host.textContent).not.toContain('当前没有这类待办')
})
it('keeps loaded rows on refresh failure and marks badge counts stale', async () => {
  await mount('/inbox?category=all')
  await wait(() => expect(host.textContent).toContain('示例供应商修订'))
  fail = true; await click('刷新待办')
  await wait(() => expect(host.textContent).toContain('待办服务暂不可用'))
  expect(host.textContent).toContain('示例供应商修订')
  expect(host.querySelector('.inbox-bell .count-badge')?.textContent).toBe('?')
})
it('filters progress by URL with replace instead of adding history entries', async () => {
  await mount('/inbox?source=detection')
  await wait(() => expect(host.textContent).toContain('进行中（1）'))
  await click('进行中（1）')
  expect(router.state.location.search).toContain('category=progress')
  expect(router.state.historyAction).toBe('REPLACE')
  expect(host.textContent).toContain('检测进行中 7/12')
})
it('refreshes at the nearest deadline and pauses regular polling when hidden', async () => {
  vi.useFakeTimers(); const now = Date.now(); fixture = inboxFixture(now)
  fixture.sources[0].items[0].deadline = new Date(now + 10_000).toISOString()
  await mount('/home')
  for (let index = 0; index < 3; index++) await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(fetcher).toHaveBeenCalledTimes(1)
  fixture.sources[0].items[0].deadline = null
  await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
  expect(fetcher).toHaveBeenCalledTimes(2)
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
  await act(async () => { document.dispatchEvent(new Event('visibilitychange')); await vi.advanceTimersByTimeAsync(120_000) })
  expect(fetcher).toHaveBeenCalledTimes(2)
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  await act(async () => { document.dispatchEvent(new Event('visibilitychange', { bubbles: true })) })
  await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
  expect(fetcher.mock.calls.length).toBeGreaterThan(2)
})
it('invalidates inbox after successful operations without changing failed operations', async () => {
  const queryClient = createQueryClient(); queryClient.setQueryData(['inbox', 'viewer'], fixture)
  const mutation = queryClient.getMutationCache().build(queryClient, { mutationFn: async () => 'saved' })
  await mutation.execute(undefined)
  expect(queryClient.getQueryState(['inbox', 'viewer'])?.isInvalidated).toBe(true)
  queryClient.setQueryData(['inbox', 'viewer'], fixture)
  const rejected = queryClient.getMutationCache().build(queryClient, { mutationFn: async () => { throw new Error('failed') } })
  await expect(rejected.execute(undefined)).rejects.toThrow('failed')
  expect(queryClient.getQueryState(['inbox', 'viewer'])?.isInvalidated).toBe(false)
  queryClient.clear()
})
it('uses the same trade terminal states and contribution action semantics', async () => {
  expect(statusMeta('trade', 'released').terminal).toBe(true)
  expect(statusMeta('trade', 'paid').terminal).toBe(false)
  expect(statusMeta('contribution', 'ready').tone).toBe('warning')
  await mount('/home')
  await act(async () => root.render(<DomainStatusBadge domain="detection" value="interrupted" />))
  expect(host.textContent).toBe('已中断')
})

it('appends reminders without treating rendered rows as the total count', async () => {
  const source = fixture.sources[4], template = source.items[0]
  source.items = Array.from({ length: 101 }, (_, index) => ({ ...template, id: `draft-${index}`, title: `示例修订 ${index}` }))
  source.counts = { action: 101, anomaly: 0, progress: 0 }
  await mount('/inbox?source=contributions')
  await wait(() => expect(host.textContent).toContain('需要我处理（101）'))
  expect(host.querySelectorAll('.inbox-list li')).toHaveLength(50)
  await click('加载更多')
  expect(host.querySelectorAll('.inbox-list li')).toHaveLength(100)
  expect(router.state.location.search).toBe('?source=contributions')
})
