// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ChannelEditorPage } from './ChannelEditorPage'
import { detail, fixtureResponse } from './detail.testdata'

let host: HTMLDivElement
let root: Root
let client: QueryClient
let saved: Record<string, unknown> | undefined
beforeEach(async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  saved = undefined
  vi.stubGlobal('fetch', vi.fn(async (input: string, options?: RequestInit) => {
    // 用慢于旧等待窗口的响应验证测试不会提前断言。
    await new Promise((resolve) => setTimeout(resolve, 35))
    if (options?.method === 'PATCH') {
      saved = JSON.parse(String(options.body))
      return new Response(JSON.stringify({ channel: detail.channel }))
    }
    return new Response(JSON.stringify(fixtureResponse(new URL(String(input), 'http://localhost'))))
  }))
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => root.render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/channels/demo']}><Routes><Route path="/channels/:id" element={<ChannelEditorPage />} /></Routes></MemoryRouter></QueryClientProvider>))
  await waitForUI(() => expect(host.querySelectorAll('.channel-failures li')).toHaveLength(3))
})
afterEach(async () => {
  await act(async () => root.unmount())
  client.clear()
  host.remove()
  vi.unstubAllGlobals()
})
async function waitForUI(assertion: () => void) {
  const deadline = Date.now() + 2000
  for (;;) {
    // 在 act 内交还事件循环，直到具体界面断言通过，超时则保留断言错误。
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
    try {
      assertion()
      return
    } catch (error) {
      if (Date.now() >= deadline) throw error
    }
  }
}
async function click(text: string, scope: ParentNode = host) {
  const button = [...scope.querySelectorAll('button')].find((item) => item.textContent === text)
  expect(button, text).toBeDefined()
  await act(async () => button!.click())
}
function panel() { return host.querySelector('[role="tabpanel"]')! }

it('opens a bounded overview, preserves the configuration draft across sections, then saves it', async () => {
  expect(host.querySelector('[aria-selected="true"]')?.textContent).toBe('概览')
  expect(panel().querySelector('form')).toBeNull()
  expect(panel().querySelectorAll('.channel-failures li')).toHaveLength(3)
  expect(panel().querySelector('.channel-events')).toBeNull()
  await click('模型与配置')
  const input = host.querySelector<HTMLInputElement>('input[maxlength="64"]')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '未保存的名称')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await click('概览')
  await click('模型与配置')
  expect(host.querySelector<HTMLInputElement>('input[maxlength="64"]')?.value).toBe('未保存的名称')
  await click('保存')
  await waitForUI(() => expect(host.querySelector('.success-message')?.textContent).toBe('已保存'))
  expect(saved?.name).toBe('未保存的名称')
  expect(saved).not.toHaveProperty('api_key')
})

it('pages calls without accumulating rows and resets pagination when filters change', async () => {
  await click('调用记录')
  await waitForUI(() => expect(host.querySelectorAll('tbody tr')).toHaveLength(20))
  await click('下一页')
  await waitForUI(() => expect(host.textContent).toContain('call-model-20'))
  expect(host.querySelectorAll('tbody tr')).toHaveLength(20)
  expect(host.textContent).not.toContain('call-model-0')
  await click('下一页')
  await waitForUI(() => expect(host.querySelectorAll('tbody tr')).toHaveLength(5))
  expect([...host.querySelectorAll('button')].find((b) => b.textContent === '下一页')?.disabled).toBe(true)
  await click('上一页')
  expect(host.querySelectorAll('tbody tr')).toHaveLength(20)
  const select = host.querySelector<HTMLSelectElement>('select[aria-label="结果"]')!
  await act(async () => { select.value = 'upstream_failed'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  await waitForUI(() => expect(host.querySelectorAll('tbody tr')).toHaveLength(9))
  expect(host.textContent).toContain('第 1 页')
})

it('opens recent anomalies separately and pages failures and health events', async () => {
  await click('查看全部异常')
  expect(host.querySelector('[aria-selected="true"]')?.textContent).toBe('健康事件')
  expect(host.querySelectorAll('.channel-failures li')).toHaveLength(5)
  expect(host.querySelectorAll('.channel-events li')).toHaveLength(10)
  await click('下一页', host.querySelector('[aria-label="最近失败分页"]')!)
  expect(host.textContent).toContain('failed-model-5')
  expect(host.textContent).not.toContain('failed-model-0')
  const nav = host.querySelector('[aria-label="健康事件分页"]')!
  await click('下一页', nav)
  await click('下一页', nav)
  await click('下一页', nav)
  expect(host.querySelectorAll('.channel-events li')).toHaveLength(3)
})

it('keeps filtered live calls bounded and ignores incomplete event rows', async () => {
  const sources: Array<{ onmessage?: (event: { data: string }) => void }> = []
  vi.stubGlobal('EventSource', class {
    onmessage?: (event: { data: string }) => void
    constructor() { sources.push(this) }
    close() {}
  })
  await click('调用记录')
  await waitForUI(() => expect(host.querySelectorAll('tbody tr')).toHaveLength(20))
  await click('下一页')
  await waitForUI(() => expect(host.textContent).toContain('第 2 页'))
  await act(async () => (host.querySelector('[role="switch"]') as HTMLElement).click())
  expect(host.textContent).toContain('第 1 页')
  await act(async () => {
    for (let i = 0; i < 30; i++) sources[0]?.onmessage?.({ data: JSON.stringify({ id: `live-${i}`, model_id: `live-model-${i}`, created_at: '2026-10-12T12:00:00Z', format: 'openai_chat', stream: true, outcome: 'succeeded', usage: {}, revenue: '1', error: null }) })
  })
  expect(host.querySelectorAll('tbody tr')).toHaveLength(20)
  expect(host.textContent).not.toContain('live-model-29')
  expect(host.textContent).toContain('call-model-0')
  expect([...host.querySelectorAll('button')].find((b) => b.textContent === '下一页')?.disabled).toBe(true)
})


it('keeps the current call page on a pagination error and allows retrying', async () => {
  await click('调用记录')
  await waitForUI(() => expect(host.querySelectorAll('tbody tr')).toHaveLength(20))
  vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify({ error: { message: '翻页请求失败' } }), { status: 500 }))
  await click('下一页')
  expect(host.textContent).toContain('第 1 页')
  expect(host.querySelectorAll('tbody tr')).toHaveLength(20)
  await waitForUI(() => expect(host.querySelector('[role="alert"]')).not.toBeNull())
  await click('下一页')
  await waitForUI(() => expect(host.textContent).toContain('第 2 页'))
  expect(host.querySelectorAll('tbody tr')).toHaveLength(20)
})
