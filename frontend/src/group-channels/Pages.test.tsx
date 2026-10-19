// @vitest-environment jsdom
import { UnsavedGuardProvider } from '../ui/UnsavedGuard'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { createMemoryRouter, Outlet, RouterProvider } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { AdminGroupChannelPage, GroupChannelEditorPage } from './Pages'
import type { GroupChannel, GroupSource } from './queries'
import { stats } from '../channels/stats.testdata'

const defaults = { user_agent: null, header_rules: null, concurrency_limit: null, rpm_limit: null, ttft_timeout_ms: null, request_timeout_ms: null, idle_timeout_ms: null, cooldown_failures: null, cooldown_seconds: null }
const source: GroupSource = { id: 'group', name: '标准分组', supplier_name: '合成供应商', revision: 1, defaults, addresses: [{ id: 'one', label: '直连', url: 'https://one.example' }, { id: 'two', label: 'CF', url: 'https://two.example' }, { id: 'three', label: 'CDN', url: 'https://three.example' }], models: [{ id: 'offering', model_id: 'vendor/model:1', display_name: '模型', model_type: 'text' }] }
const channel: GroupChannel = { id: 'demo', name: '渠道', owner: { id: 'owner', username: 'owner', display_name: '合成成员' }, summary: '', description: '', status: 'listed', suspended_reason: null, group_id: source.id, group_name: source.name, supplier_name: source.supplier_name, revision: 1, group_revision: 1, addresses: source.addresses, selected_address_ids: ['two', 'one'], overrides: defaults, effective: defaults, sources: {}, models: [{ group_model_id: 'offering', model_id: 'vendor/model:1', display_name: '模型', model_type: 'text', excluded: false, discount: '0.8', probe_state: 'available', formats: ['openai_chat'], format_tests: {}, quote_id: 'quote', enabled: true }], available: true, reason: '', created_at: '2026-10-18T00:00:00Z', updated_at: '2026-10-18T00:00:00Z' }
let host: HTMLDivElement, root: Root, client: QueryClient, saved: Record<string, unknown> | undefined
let current: GroupChannel
let router: ReturnType<typeof createMemoryRouter>
beforeEach(async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
  saved = undefined; current = structuredClone(channel)
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    if (init?.method === 'PATCH') { saved = JSON.parse(String(init.body)); current = { ...current, ...saved, selected_address_ids: saved?.address_ids ?? current.selected_address_ids, revision: current.revision + 1 } as GroupChannel; return new Response(JSON.stringify(current)) }
    if (input.endsWith('/form-defaults')) return new Response(JSON.stringify({ default_ttft_timeout_ms: 30000, default_request_timeout_ms: 600000, default_idle_timeout_ms: 600000, default_cooldown_seconds: 300, default_cooldown_failures: 3, default_max_attempts: 3 }))
    if (input.endsWith('/sources')) return new Response(JSON.stringify({ items: [source] }))
    if (input.endsWith('/stats')) return new Response(JSON.stringify(stats))
    if (input.endsWith('/events')) return new Response(JSON.stringify({ items: [] }))
    return new Response(JSON.stringify(current))
  }))
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  router = createMemoryRouter([{ element: <UnsavedGuardProvider><Outlet /></UnsavedGuardProvider>, children: [{ path: '/supply/channels/:id/*', element: <GroupChannelEditorPage /> }, { path: '/away', element: <p>Away</p> }] }], { initialEntries: ['/supply/channels/demo'] })
  await act(async () => root.render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>))
  await waitForUI(() => expect(host.querySelector('nav[aria-label="渠道详情分区"]')).not.toBeNull())
})
afterEach(async () => { await act(async () => root.unmount()); client.clear(); host.remove(); router.dispose(); vi.unstubAllGlobals(); vi.restoreAllMocks(); vi.useRealTimers() })
async function waitForUI(assertion: () => void) { const deadline = Date.now() + 2000; for (;;) { await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) }); try { assertion(); return } catch (e) { if (Date.now() >= deadline) throw e } } }
async function click(text: string) { const button = [...host.querySelectorAll<HTMLButtonElement | HTMLAnchorElement>('button,a')].find(b => b.textContent?.replace(' ●', '') === text); expect(button, text).toBeDefined(); await act(async () => button!.click()) }
async function value(label: string, next: string) { const input = ([...host.querySelectorAll('input')].find(e=>e.getAttribute('aria-label')===label) ?? [...host.querySelectorAll('label')].find((l) => l.querySelector('.field-label')?.textContent === label)?.querySelector('input,select')) as HTMLInputElement | HTMLSelectElement; expect(input, label).toBeDefined(); await act(async () => { const prototype = input instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype; Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(input, next); input.dispatchEvent(new Event(input instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true })) }) }

it('preserves address order, exclusion, zero discount, empty headers and draft across sections', async () => {
  await click('配置')
  await value('渠道名称', '未保存的渠道名')
  await value('vendor/model:1 折扣', '0')
  await click('高级设置'); await click('明确不设')
  const model = [...host.querySelectorAll('label')].find((l) => l.textContent === 'vendor/model:1')!.querySelector('input')!
  await act(async () => model.click())
  const down = host.querySelector<HTMLButtonElement>('button[aria-label="下移CF"]')!
  await act(async () => down.click())
  expect(router.state.location.pathname).toBe('/supply/channels/demo/config')
  expect([...host.querySelectorAll('.group-addresses li')].map((li) => li.textContent?.split('https:')[0])).toEqual(['直连', 'CF', 'CDN'])
  await click('概览'); await click('配置')
  expect(host.querySelector<HTMLInputElement>('input[maxlength="64"]')?.value).toBe('未保存的渠道名')
  expect([...host.querySelectorAll('button')].find((b) => b.textContent === '重新探测已保存 Key')?.disabled).toBe(true)
  await click('保存'); await waitForUI(() => expect(saved).toBeDefined())
  expect(saved).not.toHaveProperty('api_key')
  expect(saved?.address_ids).toEqual(['one', 'two'])
  expect(saved?.models).toEqual([{ group_model_id: 'offering', excluded: true, discount: '0' }])
  expect(saved?.overrides).toMatchObject({ header_rules: {} })
})

it('preserves a draft on conflict and only replaces it after explicit reload', async () => {
  await click('配置'); await value('渠道名称', '保留草稿')
  await act(async () => { current = { ...current, revision: 4, name: '其他窗口保存' }; client.setQueryData(['group-channels', 'detail', 'demo', false], current) })
  expect(host.querySelector<HTMLInputElement>('input[maxlength="64"]')?.value).toBe('保留草稿')
  await waitForUI(() => expect(host.textContent).toContain('配置版本已更新'))
  expect([...host.querySelectorAll('button')].find(b => b.textContent === '保存')?.disabled).toBe(true)
  await click('载入最新')
  vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify({ message: '载入暂时失败' }), { status: 503 }))
  await act(async () => { host.querySelector<HTMLButtonElement>('dialog[open] .modal-actions button:last-child')!.click() })
  await waitForUI(() => expect(host.querySelector('dialog[open]')?.textContent).toContain('载入暂时失败'))
  expect(host.querySelector<HTMLInputElement>('input[maxlength="64"]')?.value).toBe('保留草稿')
  await act(async () => { host.querySelector<HTMLButtonElement>('dialog[open] .modal-actions button:last-child')!.click() })
  await waitForUI(() => expect(host.querySelector<HTMLInputElement>('input[maxlength="64"]')?.value).toBe('其他窗口保存'))
  await value('渠道名称', '重新修改'); await click('保存'); await waitForUI(() => expect(saved?.revision).toBe(4))
})

it('restores overview metrics and blocks leaving the object with a dirty configuration', async () => {
  await waitForUI(() => expect(host.textContent).toContain('24 小时成功率'))
  await click('配置'); await value('渠道名称', '离开前确认')
  await act(async () => { await router.navigate('/away') })
  expect(router.state.location.pathname).toBe('/supply/channels/demo/config')
  expect(host.textContent).toContain('放弃未保存的修改')
})

it('offers reload after a save returns 409 even before the background query changes', async () => {
  await click('配置'); await value('渠道名称', '尚未提交')
  current = { ...current, revision: 3, name: '并发更新' }
  vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify({ message: '版本冲突' }), { status: 409 }))
  await click('保存'); await waitForUI(() => expect(host.textContent).toContain('载入最新'))
  expect(host.querySelector<HTMLInputElement>('input[maxlength="64"]')?.value).toBe('尚未提交')
  await click('载入最新')
  await act(async () => host.querySelector<HTMLButtonElement>('dialog[open] .modal-actions button:last-child')!.click())
  await waitForUI(() => expect(host.querySelector<HTMLInputElement>('input[maxlength="64"]')?.value).toBe('并发更新'))
  await value('渠道名称', '冲突后重试'); await click('保存'); await waitForUI(() => expect(saved?.revision).toBe(3))
})

it('rejects an empty custom header mode until the user explicitly selects no rules', async () => {
  await click('配置'); await click('高级设置'); await click('自定义')
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(saved).toBeUndefined()
  expect(host.textContent).toContain('请添加请求头规则，或选择明确不设')
  await click('明确不设'); await click('保存'); await waitForUI(() => expect(saved).toBeDefined())
  expect(saved?.overrides).toMatchObject({ header_rules: {} })
})

it('keeps the overview and current configuration usable when group options fail', async () => {
  const original = vi.mocked(fetch).getMockImplementation()!
  vi.mocked(fetch).mockImplementation((input, options) => String(input).endsWith('/sources') ? Promise.resolve(new Response(JSON.stringify({ message: '选项暂不可用' }), { status: 503 })) : original(input, options))
  await act(async () => root.render(null)); client.clear()
  await act(async () => root.render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>))
  await waitForUI(() => expect(host.textContent).toContain('分组选项加载失败'))
  await click('配置'); await value('渠道名称', '仍可保存')
  await click('保存'); await waitForUI(() => expect(saved?.name).toBe('仍可保存'))
  expect(saved?.address_ids).toEqual(['two', 'one'])
})

it('pauses probing refresh while hidden and stops on a terminal result', async () => {
  await waitForUI(() => expect(host.textContent).toContain('24 小时成功率'))
  vi.useFakeTimers()
  const count = () => vi.mocked(fetch).mock.calls.filter(([input]) => String(input).endsWith('/group-channels/demo')).length
  current = { ...current, models: current.models.map(model => ({ ...model, probe_state: 'pending' })) }
  await act(async () => client.setQueryData(['group-channels', 'detail', 'demo', false], current))
  const start = count()
  await act(async () => vi.advanceTimersByTimeAsync(5001))
  expect(count()).toBe(start + 1)
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
  await act(async () => document.dispatchEvent(new Event('visibilitychange')))
  await act(async () => vi.advanceTimersByTimeAsync(15_000)); expect(count()).toBe(start + 1)
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  current = { ...current, models: current.models.map(model => ({ ...model, probe_state: 'available' })) }
  await act(async () => document.dispatchEvent(new Event('visibilitychange')))
  await act(async () => vi.advanceTimersByTimeAsync(5001))
  const finished = count()
  await act(async () => vi.advanceTimersByTimeAsync(15_000)); expect(count()).toBe(finished)
})

it('sends governance calls to the administrator list with the channel filter', async () => {
  await act(async () => root.render(null)); router.dispose()
  router = createMemoryRouter([{ path: '/admin/channels/:id/*', element: <AdminGroupChannelPage /> }], { initialEntries: ['/admin/channels/demo'] })
  await act(async () => root.render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>))
  await waitForUI(() => expect(host.querySelector('a[href="/admin/calls?channel_id=demo"]')).not.toBeNull())
  expect(host.querySelector('a[href="/admin/channels/demo/calls"]')).toBeNull()
})
