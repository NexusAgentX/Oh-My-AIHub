// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { TypicalCostsPage } from './Page'
import type { TypicalConfig, TypicalEstimate } from './queries'

const config: TypicalConfig = { version: 1, window_days: 30, min_samples: 20, interval_seconds: 300, typical_tokens: 1000000, typical_requests: 100, typical_images: 100 }
const estimate: TypicalEstimate = { level: 'group', model_id: 'fixture/model', group_name: '合成分组', group_id: 'g', quote_version: 2, pricing_version: 'version', state: 'unknown', reason: 'missing_reliable_conversion', cost: null, request_cost: '2', request_quantity: 100, fee: null, reference_cost: null, ratio: null, unit: 'tokens', quantity: 1000000, samples: 4, own_samples: 0, source: 'model_site_fallback', cache_read_share: null, workload: { requests: 4, input_tokens: 0, output_tokens: 0, cache_write_tokens: 0, cache_read_tokens: 0, images_nano: 0, tokens: {}, service_tiers: {}, units_nano: {} } }
let host: HTMLDivElement, root: Root, client: QueryClient, saved: TypicalConfig | undefined
let running: boolean
beforeEach(async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); saved = undefined; running = false
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    if (init?.method === 'PUT') { saved = JSON.parse(String(init.body)); return new Response(JSON.stringify({ ...saved, version: 2 })) }
    if (input.includes('/settings')) return new Response(JSON.stringify(config))
    if (init?.method === 'POST') return new Response(JSON.stringify({ queued: true }), { status: 202 })
    return new Response(JSON.stringify({ snapshot: { version: 'typical.v1', config, input_version: 'v1', usd_cny_rate_nano: 7000000000, fee_rate_nano: 1000000, updated_at: '2026-10-18T00:00:00Z', window_start: '2026-09-18T00:00:00Z', watermark: '2026-10-18T00:00:00Z', reconciled_at: '2026-10-18T00:00:00Z' }, status: { running, pending: false, last_attempt: '2026-10-18T00:00:00Z', last_error: '', saturated: false }, items: [estimate], next_cursor: null }))
  }))
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  await act(async () => root.render(<QueryClientProvider client={client}><MemoryRouter><TypicalCostsPage /></MemoryRouter></QueryClientProvider>))
  await wait(() => expect(host.textContent).toContain('缺少用量换算依据'))
})
afterEach(async () => { await act(async () => root.unmount()); client.clear(); host.remove(); vi.unstubAllGlobals() })
async function wait(check: () => void) { const deadline = Date.now() + 2000; for (;;) { await act(async () => { await new Promise((r) => setTimeout(r, 0)) }); try { check(); return } catch (e) { if (Date.now() >= deadline) throw e } } }
async function input(label: string, value: string) {
  const field = [...host.querySelectorAll('label')].find((l) => l.querySelector('.field-label')?.textContent === label)!.querySelector('input')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(field, value); field.dispatchEvent(new Event('input', { bubbles: true })) })
}
it('shows unknown conversion, separate request estimate, source and versions without a fake free price', () => {
  expect(host.textContent).toContain('站内同模型结构推算')
  expect(host.textContent).toContain('报价版本 2')
  expect(host.textContent).toContain('2 积分')
  expect(host.textContent).not.toContain('0 积分')
  expect(host.textContent).toContain('1 美元 = 7 积分')
})
it('rejects zero or fractional settings and saves an optimistic version', async () => {
  await input('样本窗口（天）', '0')
  const save = [...host.querySelectorAll('button')].find((b) => b.textContent === '保存参数')!
  expect(save.disabled).toBe(true)
  await input('样本窗口（天）', '7.5'); expect(save.disabled).toBe(true)
  await input('样本窗口（天）', '7')
  await act(async () => save.closest('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await wait(() => expect(saved?.window_days).toBe(7))
  expect(saved?.version).toBe(1)
  await wait(() => expect(host.textContent).toContain('已保存'))
})
it('keeps previous results while an update runs and disables duplicate update actions', async () => {
  running = true
  await act(async () => { await client.invalidateQueries({ queryKey: ['typical-costs'] }) })
  await wait(() => expect(host.textContent).toContain('正在更新，期间保留上次结果'))
  expect([...host.querySelectorAll('button')].find((b) => b.textContent === '立即更新')?.disabled).toBe(true)
  expect(host.textContent).toContain('fixture/model')
})
