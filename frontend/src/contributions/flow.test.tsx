// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { createMemoryRouter, Outlet, RouterProvider } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ContributionDetailPage } from './Pages'
import { NewDetectionPage } from '../detection/Pages'
import { UnsavedGuardProvider } from '../ui/UnsavedGuard'
import { contribution, draftID, targetID } from './fixtures.testdata'

vi.mock('../auth/AuthProvider', () => ({ useAuth: () => ({ account: { is_admin: false } }) }))
let host: HTMLDivElement, root: Root, client: QueryClient, router: ReturnType<typeof createMemoryRouter>
let current: typeof contribution, saved: Record<string, unknown> | undefined, rejectSave: boolean, failReload: boolean
beforeEach(async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
  current = structuredClone(contribution); saved = undefined; rejectSave = false; failReload = false
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input, 'http://localhost')
    if (init?.method === 'PUT') {
      if (rejectSave) return new Response(JSON.stringify({ error: 'conflict', message: '版本冲突' }), { status: 409 })
      saved = JSON.parse(String(init.body)); current.draft = { ...current.draft, ...saved, revision: current.draft.revision + 1 } as typeof current.draft
      return new Response(JSON.stringify({ draft: current.draft }))
    }
    if (url.pathname.endsWith('/reports')) return new Response(JSON.stringify({ items: [{ id: 'report-one', complete: true, created_at: '2026-10-19T02:00:00Z' }], next_cursor: null }))
    if (url.pathname === '/api/detection/jobs') {
      if (init?.method === 'POST') return new Response(JSON.stringify({ job: { id: 'new-task' } }), { status: 202 })
      return new Response(JSON.stringify({ items: [{ id: 'matching-task', target: { binding: { target_id: targetID } }, created_at: '2026-10-19T02:00:00Z', finished_scenarios: 3, scenarios: 3 }, { id: 'other-task', target: { binding: { target_id: 'different' } }, created_at: '2026-10-18T02:00:00Z' }], next_cursor: null }))
    }
    return failReload ? new Response(JSON.stringify({ error: 'unavailable', message: '暂时不可用' }), { status: 503 }) : new Response(JSON.stringify(current))
  }))
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  router = createMemoryRouter([{ element: <UnsavedGuardProvider><Outlet /></UnsavedGuardProvider>, children: [{ path: '/supply/contributions/:draftID', element: <ContributionDetailPage /> }, { path: '/supply/detection/new', element: <NewDetectionPage /> }, { path: '/supply/detection/:taskID', element: <p>任务已创建</p> }, { path: '/away', element: <p>Away</p> }] }], { initialEntries: [`/supply/contributions/${draftID}`] })
  await act(async () => root.render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>))
  await waitForUI(() => expect(field('标题')).toBeDefined())
})
afterEach(async () => { await act(async () => root.unmount()); client.clear(); router.dispose(); host.remove(); vi.unstubAllGlobals(); vi.restoreAllMocks() })
async function waitForUI(assertion: () => void) { const deadline = Date.now() + 2000; for (;;) { await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) }); try { assertion(); return } catch (error) { if (Date.now() > deadline) throw error } } }
function field(label: string) { return [...host.querySelectorAll('label')].find(item => item.querySelector('.field-label')?.textContent === label)?.querySelector<HTMLInputElement | HTMLTextAreaElement>('input,textarea') }
async function fill(label: string, value: string) { const input = field(label)!; expect(input, label).toBeDefined(); await act(async () => { Object.getOwnPropertyDescriptor(input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) }) }
async function click(text: string) { const button = [...host.querySelectorAll<HTMLButtonElement | HTMLAnchorElement>('button,a')].find(item => item.textContent === text)!; expect(button, text).toBeDefined(); await act(async () => button.click()) }

it('preserves unpriced, explicit free and decimal precision through save; blocks unsaved navigation', async () => {
  await fill('输入单价', '0.000000001')
  await act(async () => router.navigate('/away'))
  expect(host.querySelector('dialog[open]')?.textContent).toContain('放弃未保存')
  await click('取消')
  await click('保存')
  await waitForUI(() => expect(saved).toBeDefined())
  const changes = saved!.changes as typeof current.draft.changes
  expect(changes.quotes[0].prices).toEqual({ input: '0.000000001', output: '0', cache_write: '', cache_read: '', details: {} })
  await waitForUI(() => expect(host.textContent).toContain('已保存 ·'))
})
it('keeps local input after a save conflict and failed reload, then loads only after confirmation', async () => {
  await fill('标题', '我的未保存标题'); rejectSave = true
  await click('保存')
  await waitForUI(() => expect(host.textContent).toContain('草稿已被更新'))
  await click('载入最新'); failReload = true
  await act(async () => host.querySelector<HTMLButtonElement>('dialog[open] button.button-primary')!.click())
  await waitForUI(() => expect(host.querySelector('dialog[open]')?.textContent).toContain('暂时不可用'))
  expect(field('标题')?.value).toBe('我的未保存标题')
  failReload = false; current.draft.title = '其他人保存的标题'; current.draft.revision++
  const confirm = host.querySelector<HTMLButtonElement>('dialog[open] button.button-primary')!
  await act(async () => confirm.click())
  await waitForUI(() => expect(field('标题')?.value).toBe('其他人保存的标题'))
})
it('rejects malformed raw pricing without crashing or enabling save', async () => {
  await click('高级·报价原始 JSON')
  await fill('报价原始 JSON', '{"prices":null,"tiers":[],"unit_prices":[]}')
  expect(host.textContent).toContain('请输入格式与字段类型正确的 JSON')
  expect([...host.querySelectorAll('button')].find(button => button.textContent === '保存')?.disabled).toBe(true)
})
it('keeps report selection within its target and saves the selected immutable report ID', async () => {
  await click('选择此目标的报告')
  await waitForUI(() => expect(host.querySelector('option[value="matching-task"]')).not.toBeNull())
  expect(host.querySelector('option[value="other-task"]')).toBeNull()
  const select = host.querySelector<HTMLSelectElement>('select')!
  // The model-type and currency selects precede the task selector.
  const taskSelect = [...host.querySelectorAll('select')].find(item => item.querySelector('option[value="matching-task"]')) ?? select
  await act(async () => { taskSelect.value = 'matching-task'; taskSelect.dispatchEvent(new Event('change', { bubbles: true })) })
  await waitForUI(() => expect(host.querySelector('input[type="checkbox"]:not(:disabled)')).not.toBeNull())
  await waitForUI(() => expect(host.textContent).toContain('完整报告'))
  const checkbox = [...host.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')].find(input => input.closest('label')?.textContent?.includes('完整报告'))!
  await act(async () => checkbox.click())
  await click('保存'); await waitForUI(() => expect(saved?.report_ids).toEqual(['report-one']))
})
it('starts from the dedicated page and returns a new task to its originating contribution', async () => {
  await click('开始全测')
  expect(router.state.location.pathname).toBe('/supply/detection/new')
  expect(host.textContent).toContain('检测会调用上游并产生费用')
  await fill('本次检测使用的上游 API Key', 'synthetic-secret')
  await click('开始全量检测')
  await waitForUI(() => expect(router.state.location.pathname).toBe('/supply/detection/new-task'))
  expect(router.state.location.state.from.to).toBe(`/supply/contributions/${draftID}`)
  expect(JSON.stringify(router.state.location.state)).not.toContain('synthetic-secret')
  expect(JSON.stringify(client.getMutationCache().getAll().map(mutation => mutation.state.variables))).not.toContain('synthetic-secret')
})

it('does not roll a saved draft back when the subsequent detail refresh fails', async () => {
  await fill('标题', '已成功保存的新标题'); failReload = true
  await click('保存')
  await waitForUI(() => expect(saved?.title).toBe('已成功保存的新标题'))
  await waitForUI(() => expect(host.textContent).toContain('提交条件尚未刷新'))
  expect(field('标题')?.value).toBe('已成功保存的新标题')
  expect(host.textContent).not.toContain('草稿已被更新，请载入最新')
  expect([...host.querySelectorAll('button')].find(button => button.textContent === '提交审核')?.disabled).toBe(true)
})
