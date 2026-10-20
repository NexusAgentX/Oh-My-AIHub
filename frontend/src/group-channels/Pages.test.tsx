// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { GroupChannelEditorPage } from './Pages'
import type { GroupChannel, GroupSource } from './queries'
import { stats } from '../channels/stats.testdata'

const defaults = { user_agent: null, header_rules: null, concurrency_limit: null, rpm_limit: null, ttft_timeout_ms: null, request_timeout_ms: null, idle_timeout_ms: null, cooldown_failures: null, cooldown_seconds: null }
const source: GroupSource = { id: 'group', name: '标准分组', supplier_name: '合成供应商', revision: 1, defaults, addresses: [{ id: 'one', label: '直连', url: 'https://one.example' }, { id: 'two', label: 'CF', url: 'https://two.example' }, { id: 'three', label: 'CDN', url: 'https://three.example' }], models: [{ id: 'offering', model_id: 'vendor/model:1', display_name: '模型', model_type: 'text' }] }
const channel: GroupChannel = { id: 'demo', name: '渠道', owner: { id: 'owner', username: 'owner', display_name: '合成成员' }, summary: '', description: '', status: 'listed', suspended_reason: null, group_id: source.id, group_name: source.name, supplier_name: source.supplier_name, revision: 1, group_revision: 1, addresses: source.addresses, selected_address_ids: ['two', 'one'], overrides: defaults, effective: defaults, sources: {}, models: [{ group_model_id: 'offering', model_id: 'vendor/model:1', display_name: '模型', model_type: 'text', excluded: false, discount: '0.8', probe_state: 'available', formats: ['openai_chat'], format_tests: {}, quote_id: 'quote', enabled: true }], available: true, reason: '', created_at: '2026-10-18T00:00:00Z', updated_at: '2026-10-18T00:00:00Z' }
let host: HTMLDivElement, root: Root, client: QueryClient, saved: Record<string, unknown> | undefined
let current: GroupChannel
beforeEach(async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  saved = undefined; current = structuredClone(channel)
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    if (init?.method === 'PATCH') { saved = JSON.parse(String(init.body)); current = { ...current, revision: 2 }; return new Response(JSON.stringify(current)) }
    if (input.endsWith('/sources')) return new Response(JSON.stringify({ items: [source] }))
    if (input.endsWith('/stats')) return new Response(JSON.stringify(stats))
    if (input.endsWith('/events')) return new Response(JSON.stringify({ items: [] }))
    return new Response(JSON.stringify(current))
  }))
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  await act(async () => root.render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/group-channels/demo']}><Routes><Route path="/group-channels/:id" element={<GroupChannelEditorPage />} /></Routes></MemoryRouter></QueryClientProvider>))
  await waitForUI(() => expect(host.querySelector('[role="tablist"]')).not.toBeNull())
})
afterEach(async () => { await act(async () => root.unmount()); client.clear(); host.remove(); vi.unstubAllGlobals() })
async function waitForUI(assertion: () => void) { const deadline = Date.now() + 2000; for (;;) { await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) }); try { assertion(); return } catch (e) { if (Date.now() >= deadline) throw e } } }
async function click(text: string) { const button = [...host.querySelectorAll('button')].find((b) => b.textContent === text); expect(button, text).toBeDefined(); await act(async () => button!.click()) }
async function value(label: string, next: string) { const input = ([...host.querySelectorAll('input')].find(e=>e.getAttribute('aria-label')===label) ?? [...host.querySelectorAll('label')].find((l) => l.querySelector('.field-label')?.textContent === label)?.querySelector('input,select')) as HTMLInputElement | HTMLSelectElement; expect(input, label).toBeDefined(); await act(async () => { const prototype = input instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype; Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(input, next); input.dispatchEvent(new Event(input instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true })) }) }

it('preserves address order, exclusion, zero discount, empty headers and draft across sections', async () => {
  await click('模型与配置')
  await value('渠道名称', '未保存的渠道名')
  await value('vendor/model:1 折扣', '0')
  await value('请求头规则', 'empty')
  const model = [...host.querySelectorAll('label')].find((l) => l.textContent === 'vendor/model:1')!.querySelector('input')!
  await act(async () => model.click())
  const down = host.querySelector<HTMLButtonElement>('button[aria-label="下移CF"]')!
  await act(async () => down.click())
  expect([...host.querySelectorAll('.group-addresses li')].map((li) => li.textContent?.split('https:')[0])).toEqual(['直连', 'CF', 'CDN'])
  await click('概览'); await click('模型与配置')
  expect(host.querySelector<HTMLInputElement>('input[maxlength="64"]')?.value).toBe('未保存的渠道名')
  expect([...host.querySelectorAll('button')].find((b) => b.textContent === '重新探测已保存 Key')?.disabled).toBe(true)
  await click('保存配置'); await waitForUI(() => expect(saved).toBeDefined())
  expect(saved).not.toHaveProperty('api_key')
  expect(saved?.address_ids).toEqual(['one', 'two'])
  expect(saved?.models).toEqual([{ group_model_id: 'offering', excluded: true, discount: '0' }])
  expect(saved?.overrides).toMatchObject({ header_rules: {} })
})

it('keeps an unsaved draft and its revision when a newer configuration arrives', async () => {
  await click('模型与配置'); await value('渠道名称', '保留草稿')
  await act(async () => { current = { ...current, revision: 4, name: '其他窗口保存' }; client.setQueryData(['group-channels', 'detail', 'demo', false], current) })
  expect(host.querySelector<HTMLInputElement>('input[maxlength="64"]')?.value).toBe('保留草稿')
  await waitForUI(()=>expect(host.textContent).toContain('配置版本已更新'))
  await click('保存配置'); await waitForUI(() => expect(saved).toBeDefined())
  expect(saved?.revision).toBe(1)
})
