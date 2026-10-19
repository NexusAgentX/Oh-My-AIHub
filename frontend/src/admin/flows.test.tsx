// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { createMemoryRouter, Outlet, RouterProvider } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { NewUserRoute, UserRoute, UsersPage } from './UsersPage'
import { AdminTransactionsPage, PointsPage } from './PointsPage'
import { AdminTradeDetailPage, AdminTradesPage } from './TradesPage'
import { CatalogPage } from './CatalogPage'
import { NewContributionRoute } from '../contributions/Pages'
import { SettingsPage } from './SettingsPage'
import { UnsavedGuardProvider } from '../ui/UnsavedGuard'
import { clearSecretHandoffs } from '../ui/secretHandoff'
import { account, groupID, settings, supplierID, trade, transaction } from './fixtures.testdata'

vi.mock('../auth/AuthProvider', () => ({ useAuth: () => ({ account: { id: 'viewer-admin', is_admin: true } }) }))
let host: HTMLDivElement, root: Root, client: QueryClient, router: ReturnType<typeof createMemoryRouter>
let current = structuredClone(account), currentTrade = structuredClone(trade), posted: { path: string; body: Record<string, unknown> }[], failDetail: boolean
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
  current = structuredClone(account); currentTrade = structuredClone(trade); posted = []; failDetail = false
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input, 'http://localhost'), method = init?.method || 'GET'
    const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status })
    if (method !== 'GET') {
      const body = init?.body ? JSON.parse(String(init.body)) : {}; posted.push({ path: url.pathname, body })
      if (url.pathname === '/api/admin/accounts') return json({ account: current, initial_password: 'synthetic-one-time-password' }, 201)
      if (method === 'PATCH') { current = { ...current, ...body }; return json({ account: current }) }
      if (url.pathname.endsWith('/resolve')) { currentTrade = { ...currentTrade, status: 'resolved_to_buyer' }; return json({ trade: currentTrade }) }
      if (url.pathname === '/api/admin/settings') return json({ settings: { ...settings, ...body } })
      return json({ account: current })
    }
    if (url.pathname === '/api/admin/settings') return json({ settings })
    if (url.pathname === `/api/admin/accounts/${account.id}`) return failDetail ? json({ message: '资料刷新失败' }, 503) : json({ account: current })
    if (url.pathname === '/api/admin/accounts') return json({ items: [{ ...current, id: 'another-row', display_name: '其他成员' }], next_cursor: null })
    if (url.pathname === '/api/admin/c2c/trades') return json({ items: [currentTrade], next_cursor: null })
    if (url.pathname === `/api/c2c/trades/${trade.id}`) return json({ trade: currentTrade })
    if (url.pathname === '/api/admin/ledger/transactions') return json({ items: [transaction], next_cursor: null })
    if (url.pathname === `/api/admin/ledger/transactions/${transaction.id}`) return json({ transaction })
    if (url.pathname === '/api/contributions/targets') return json({ items: [{ id: groupID, parent_id: supplierID, name: '示例分组', enabled: true }], next_cursor: null })
    return json({ items: [], next_cursor: null })
  }))
})
afterEach(async () => { if (root) await act(async () => root.unmount()); client?.clear(); router?.dispose(); host?.remove(); clearSecretHandoffs(); vi.unstubAllGlobals(); vi.restoreAllMocks() })
async function mount(url: string) {
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } })
  router = createMemoryRouter([{ element: <UnsavedGuardProvider><Outlet /></UnsavedGuardProvider>, children: [
    { path: '/admin/users', element: <UsersPage />, children: [{ path: 'new', element: <NewUserRoute /> }, { path: ':id', element: <UserRoute /> }] },
    { path: '/admin/points', element: <PointsPage /> }, { path: '/admin/points/transactions', element: <AdminTransactionsPage /> },
    { path: '/admin/trades', element: <AdminTradesPage /> }, { path: '/admin/trades/:tradeID', element: <AdminTradeDetailPage /> },
    { path: '/admin/catalog', element: <CatalogPage />, children: [{ path: 'new', element: <NewContributionRoute /> }] },
    { path: '/admin/settings', element: <SettingsPage /> }, { path: '/away', element: <p>Away</p> },
  ] }], { initialEntries: [url] })
  await act(async () => root.render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>))
}
async function waitForUI(assertion: () => void) { const deadline = Date.now() + 2000; for (;;) { await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) }); try { assertion(); return } catch (error) { if (Date.now() > deadline) throw error } } }
function field(label: string) { return [...host.querySelectorAll('label')].find(item => item.querySelector('.field-label')?.textContent === label)?.querySelector<HTMLInputElement>('input') }
async function fill(label: string, value: string) { const input = field(label)!; expect(input, label).toBeDefined(); await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) }) }
async function click(text: string) { const button = [...host.querySelectorAll<HTMLButtonElement | HTMLAnchorElement>('button,a')].find(item => item.textContent === text)!; expect(button, text).toBeDefined(); await act(async () => button.click()) }

it('loads a deep-linked user independently of the first list page and preserves filters on closing', async () => {
  await mount(`/admin/users/${account.id}?q=search`)
  await waitForUI(() => expect(field('显示名')?.value).toBe(account.display_name))
  expect(host.textContent).toContain('可用额度150.00')
  await act(async () => host.querySelector<HTMLButtonElement>('.drawer button[aria-label="关闭"]')!.click())
  expect(router.state.location.pathname + router.state.location.search).toBe('/admin/users?q=search')
})
it('keeps a new-user form when closing is cancelled, then shows a password once without caching it', async () => {
  await mount('/admin/users/new')
  await waitForUI(() => expect(field('用户名')).toBeDefined())
  await fill('用户名', 'new-member'); await fill('显示名', '新成员')
  await act(async () => host.querySelector<HTMLButtonElement>('dialog[open] button[aria-label="关闭"]')!.click())
  await waitForUI(() => expect(host.textContent).toContain('放弃未保存的修改'))
  await click('取消'); expect(field('显示名')?.value).toBe('新成员')
  await click('创建并生成初始密码')
  await waitForUI(() => expect(router.state.location.pathname).toBe('/admin/users'))
  expect(host.textContent).toContain('synthetic-one-time-password')
  expect(JSON.stringify(client.getMutationCache().getAll().map(item => item.state.data))).not.toContain('synthetic-one-time-password')
  expect(JSON.stringify(router.state.location.state)).not.toContain('synthetic-one-time-password')
  await click('我已线下交付，关闭'); expect(host.textContent).not.toContain('synthetic-one-time-password')
})
it('keeps edits on navigation cancellation and a successful save when the readback fails', async () => {
  await mount(`/admin/users/${account.id}`)
  await waitForUI(() => expect(field('显示名')).toBeDefined())
  await fill('显示名', '本地新名称')
  await act(async () => router.navigate('/away')); await click('取消')
  expect(field('显示名')?.value).toBe('本地新名称')
  failDetail = true; await click('保存')
  await waitForUI(() => expect(posted.some(item => item.body.display_name === '本地新名称')).toBe(true))
  await waitForUI(() => expect(host.textContent).toContain('已保存 ·'))
  expect(field('显示名')?.value).toBe('本地新名称')
})
it('keeps ledger filters in URL, opens a transaction with push, and Back closes it', async () => {
  await mount('/admin/points/transactions?type=c2c_list')
  await waitForUI(() => expect(host.querySelector('.data-table')).not.toBeNull())
  const opener = host.querySelector<HTMLButtonElement>('.data-table .link-button')!
  await act(async () => opener.click())
  expect(new URLSearchParams(router.state.location.search).get('tx')).toBe(transaction.id)
  expect(new URLSearchParams(router.state.location.search).get('type')).toBe('c2c_list')
  await act(async () => router.navigate(-1))
  expect(router.state.location.search).toBe('?type=c2c_list')
  expect(host.querySelector('dialog[open]')).toBeNull()
})
it('does not broaden an incomplete related-object filter into an unfiltered ledger query', async () => {
  await mount('/admin/points/transactions?related_type=call&related_id=partial')
  await waitForUI(() => expect(host.textContent).toContain('请输入完整的关联对象 ID后查看交易'))
  expect(vi.mocked(fetch).mock.calls.some(([url]) => String(url).startsWith('/api/admin/ledger/transactions'))).toBe(false)
})
it('redirects old ledger tab and transaction parameters without losing its filters or anchor', async () => {
  await mount(`/admin/points?tab=transactions&transaction=${transaction.id}&account_id=${account.id}#checks`)
  await waitForUI(() => expect(router.state.location.pathname).toBe('/admin/points/transactions'))
  const params = new URLSearchParams(router.state.location.search)
  expect(params.get('tx')).toBe(transaction.id); expect(params.get('account_id')).toBe(account.id); expect(params.has('tab')).toBe(false)
  expect(router.state.location.hash).toBe('#checks')
})
it('shows ordinary trades read-only and requires reason plus confirmation for arbitration', async () => {
  await mount(`/admin/trades/${trade.id}`)
  await waitForUI(() => expect(host.textContent).toContain('只读'))
  expect([...host.querySelectorAll('button')].some(button => button.textContent === '判给买家')).toBe(false)
  currentTrade = { ...currentTrade, status: 'disputed', disputed_at: currentTrade.created_at }
  await act(async () => { await client.invalidateQueries({ queryKey: ['admin', 'trade'] }) })
  await waitForUI(() => expect([...host.querySelectorAll('button')].some(button => button.textContent === '判给买家')).toBe(true))
  await click('判给买家'); await click('下一步')
  expect(host.textContent).toContain('请填写原因'); expect(posted).toHaveLength(0)
  const textarea = host.querySelector<HTMLTextAreaElement>('textarea')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(textarea, '合成测试原因'); textarea.dispatchEvent(new Event('input', { bubbles: true })) })
  await click('下一步'); expect(posted).toHaveLength(0)
  await click('确认判给买家')
  await waitForUI(() => expect(posted[0]?.body).toMatchObject({ result: 'buyer', reason: '合成测试原因' }))
})
it('keeps catalog context under a prefilled revision dialog and closes back to the filtered directory', async () => {
  await mount(`/admin/catalog?kind=group&parent_id=${supplierID}`)
  await waitForUI(() => expect(host.querySelector('.data-table')).not.toBeNull())
  await click('修改')
  expect(router.state.location.pathname).toBe('/admin/catalog/new')
  await waitForUI(() => expect(host.querySelector('dialog[open]')).not.toBeNull())
  const values = [...host.querySelectorAll<HTMLSelectElement>('dialog[open] select')].map(select => select.value)
  expect(values).toEqual(['group', 'edit', groupID])
  await act(async () => host.querySelector<HTMLButtonElement>('dialog[open] button[aria-label="关闭"]')!.click())
  expect(router.state.location.pathname + router.state.location.search).toBe(`/admin/catalog?kind=group&parent_id=${supplierID}`)
})
it('protects unsaved platform settings when its query refreshes or navigation is cancelled', async () => {
  await mount('/admin/settings')
  await waitForUI(() => expect(field('手续费率（%）')).toBeDefined())
  await fill('手续费率（%）', '0.5')
  await act(async () => { client.setQueryData(['admin', 'settings'], { settings: { ...settings, fee_rate_nano: 2000000 } }) })
  expect(field('手续费率（%）')?.value).toBe('0.5')
  await act(async () => router.navigate('/away')); await click('取消')
  expect(field('手续费率（%）')?.value).toBe('0.5')
})
