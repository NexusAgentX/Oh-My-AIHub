// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import { QuotePricesEditor, validRawQuote, formatMinute } from './PriceEditors'
import { DefaultsEditor } from './DefaultsEditor'
import { emptyOverrides } from '../settings/overrides'
import { quote } from './fixtures.testdata'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })
async function mount(defaults = false) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ default_ttft_timeout_ms: 30000, default_request_timeout_ms: 600000, default_idle_timeout_ms: 600000 }))))
  let current = structuredClone(quote), bad = false
  function Test() { const [value, setValue] = useState(current), [settings, setSettings] = useState(emptyOverrides); return defaults ? <DefaultsEditor value={settings} onChange={setSettings} onInvalid={next => { bad = next }} /> : <QuotePricesEditor value={value} onChange={next => { current = next; setValue(next) }} onInvalid={next => { bad = next }} /> }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host), client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  await act(async () => root.render(<QueryClientProvider client={client}><Test /></QueryClientProvider>))
  const click = async (text: string) => { const button = [...host.querySelectorAll('button')].find(item => item.textContent?.startsWith(text))!; expect(button, text).toBeDefined(); await act(async () => button.click()) }
  const fill = async (label: string, value: string) => { const input = [...host.querySelectorAll('label')].find(item => item.querySelector('.field-label')?.textContent === label)!.querySelector<HTMLInputElement | HTMLTextAreaElement>('input,textarea')!; expect(input, label).toBeDefined(); await act(async () => { Object.getOwnPropertyDescriptor(input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) }) }
  return { host, click, fill, value: () => current, bad: () => bad, close: async () => { await act(async () => root.unmount()); client.clear(); host.remove() } }
}
it('keeps small decimal prices as strings and accepts an end-of-day boundary', async () => {
  const ui = await mount()
  try {
    await ui.fill('输入单价', '0.000000001'); expect(ui.value().prices.input).toBe('0.000000001')
    await ui.click('条件价格'); await ui.click('添加条件价'); await ui.click('星期与时段')
    const midnight = [...ui.host.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')].find(input => input.closest('label')?.textContent === '当天 24:00')!
    await act(async () => midnight.click())
    expect(ui.value().tiers[0].conditions.end_minute).toBe(1440)
    expect(formatMinute(0)).toBe('00:00'); expect(formatMinute(1440)).toBe('24:00'); expect(formatMinute(null)).toBe('')
  } finally { await ui.close() }
})
it('validates raw structures and ignores attempts to alter quote identity through price JSON', async () => {
  const ui = await mount()
  try {
    expect(validRawQuote({ prices: null, tiers: [], unit_prices: [] })).toBe(false)
    expect(validRawQuote({ ...quote, unit_prices: [{ unit: 'toString', tool: '', image_size: '', image_quality: '', image_background: '', service_tier: '', price: '' }] })).toBe(false)
    await ui.click('高级·报价原始 JSON')
    await ui.fill('报价原始 JSON', JSON.stringify({ prices: quote.prices, tiers: [], unit_prices: [], id: 'changed-id' }))
    expect(ui.value().id).toBe(quote.id)
    await ui.fill('报价原始 JSON', '{"prices":{},"tiers":[],"unit_prices":[]}')
    expect(ui.bad()).toBe(true)
  } finally { await ui.close() }
})
it('does not silently turn an empty custom-header choice into explicit clearing', async () => {
  const ui = await mount(true)
  try {
    await ui.click('高级设置'); await ui.click('自定义')
    expect(ui.bad()).toBe(true)
    await ui.click('设置请求头'); await ui.fill('请求头名称', 'X-Example'); await ui.fill('请求头值', 'demo')
    expect(ui.bad()).toBe(false)
    await ui.click('明确不设'); expect(ui.bad()).toBe(false)
    await ui.click('高级·默认设置原始 JSON'); await ui.fill('默认设置原始 JSON', '{"header_rules":[]}')
    expect(ui.bad()).toBe(true)
    await ui.fill('User-Agent', 'example'); expect(ui.bad()).toBe(true)
  } finally { await ui.close() }
})
