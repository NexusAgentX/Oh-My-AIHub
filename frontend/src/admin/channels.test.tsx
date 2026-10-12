// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ChannelDetailPage } from './ChannelsPage'
import { detail, stats } from '../channels/detail.testdata'
import type { AdminChannel } from './types'

const channel: AdminChannel = {
  ...detail.channel,
  owner: { id: 'owner', display_name: '示例渠道主', username: 'example' },
}
let host: HTMLDivElement
let root: Root
let client: QueryClient
let statsFailed = false
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  statsFailed = false
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    await new Promise((resolve) => setTimeout(resolve, 35))
    if (String(input).endsWith('/stats')) return statsFailed
      ? new Response(JSON.stringify({ error: { message: '统计暂不可用' } }), { status: 500 })
      : new Response(JSON.stringify(stats))
    return new Response(JSON.stringify({ channel, events: detail.events }))
  }))
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})
afterEach(async () => {
  await act(async () => root.unmount())
  client.clear()
  host.remove()
  vi.unstubAllGlobals()
})
async function render() {
  await act(async () => root.render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/admin/channels/demo']}>
        <Routes><Route path="/admin/channels/:channelID" element={<ChannelDetailPage />} /></Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  ))
}
async function waitForUI(assertion: () => void) {
  const deadline = Date.now() + 2000
  for (;;) {
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
    try { assertion(); return } catch (error) { if (Date.now() >= deadline) throw error }
  }
}
async function click(text: string, scope: ParentNode = host) {
  const button = [...scope.querySelectorAll('button')].find((item) => item.textContent === text)
  expect(button, text).toBeDefined()
  await act(async () => button!.click())
}

it('bounds the overview and health lists while retaining configuration and channel-scoped calls', async () => {
  await render()
  await waitForUI(() => expect(host.querySelectorAll('.channel-failures li')).toHaveLength(3))
  expect(host.querySelector('[aria-selected="true"]')?.textContent).toBe('概览')
  expect(host.textContent).toContain('示例渠道主')
  expect(host.textContent).not.toContain('配置摘要')
  expect(host.querySelector('.channel-events')).toBeNull()
  expect(host.querySelector('a[href="/admin/calls?channel_id=demo"]')).not.toBeNull()
  await click('查看全部异常')
  expect(host.querySelector('[aria-selected="true"]')?.textContent).toBe('健康事件')
  expect(host.querySelectorAll('.channel-failures li')).toHaveLength(5)
  expect(host.querySelectorAll('.channel-events li')).toHaveLength(10)
  await click('下一页', host.querySelector('[aria-label="最近失败分页"]')!)
  expect(host.textContent).toContain('failed-model-5')
  expect(host.textContent).not.toContain('failed-model-0')
  const events = host.querySelector('[aria-label="健康事件分页"]')!
  await click('下一页', events)
  await click('下一页', events)
  await click('下一页', events)
  expect(host.querySelectorAll('.channel-events li')).toHaveLength(3)
  await click('上一页', events)
  expect(host.querySelectorAll('.channel-events li')).toHaveLength(10)
  await click('模型与配置')
  expect(host.querySelectorAll('tbody tr')).toHaveLength(7)
  expect(host.textContent).toContain('https://upstream.example/v1')
  expect(host.textContent).toContain('配置摘要')
  expect(host.querySelector('.channel-failures')).toBeNull()
})

it('keeps configuration and moderation accessible when statistics fail, without submitting on dialog open', async () => {
  statsFailed = true
  await render()
  await waitForUI(() => expect(host.textContent).toContain('请求失败，请稍后重试'))
  await click('模型与配置')
  expect(host.querySelectorAll('tbody tr')).toHaveLength(7)
  await click('强制下架')
  const dialog = document.querySelector('dialog')!
  expect(dialog).not.toBeNull()
  expect(dialog.textContent).toContain('原因')
  expect(dialog.textContent).toContain('下一步')
  expect(vi.mocked(fetch).mock.calls.every(([, options]) => !options?.method || options.method === 'GET')).toBe(true)
})
