// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { CallExplorer } from './CallExplorer'
import { networkFixture } from './network.fixture'
import { summaryColumns } from './CallTable'
import { useCallStream } from './useCallStream'

class Source extends EventTarget {
  static instances: Source[] = []
  onmessage: ((event: MessageEvent) => void) | null = null
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false
  constructor(public url: string) { super(); Source.instances.push(this) }
  close() { this.closed = true }
  send(kind: string, item: unknown) { this.dispatchEvent(new MessageEvent(kind, { data: JSON.stringify(item) })) }
}
let host: HTMLDivElement, root: Root
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  Source.instances = []
  vi.stubGlobal('EventSource', Source)
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  Object.defineProperty(window, 'scrollY', { configurable: true, value: 0 })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
})
afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('receives named gateway events, updates in place, and pauses when hidden', async () => {
  function Feed() {
    const stream = useCallStream<{ id: string; outcome: string }>('/api/calls/stream')
    return <output>{stream.status} {stream.items.map(item => `${item.id}:${item.outcome}`).join(',')}</output>
  }
  await act(async () => root.render(<Feed />))
  const source = Source.instances[0]
  await act(async () => {
    source.onopen?.()
    source.send('call.started', { id: 'a', outcome: 'in_progress' })
    source.send('call.finished', { id: 'a', outcome: 'succeeded' })
  })
  expect(host.textContent).toBe('open a:succeeded')
  await act(async () => {
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
    document.dispatchEvent(new Event('visibilitychange'))
  })
  expect(source.closed).toBe(true)
  expect(host.textContent).toBe('off ')
  await act(async () => {
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
    document.dispatchEvent(new Event('visibilitychange'))
  })
  expect(Source.instances).toHaveLength(2)
})

it('stops automatic reconnection after three retries', async () => {
  vi.useFakeTimers()
  function Feed() { return <output>{useCallStream('/api/calls/stream').status}</output> }
  await act(async () => root.render(<Feed />))
  for (const delay of [1000, 2000, 4000]) {
    await act(async () => Source.instances.at(-1)!.onerror?.())
    await act(async () => vi.advanceTimersByTime(delay))
  }
  await act(async () => Source.instances.at(-1)!.onerror?.())
  await act(async () => vi.advanceTimersByTime(60_000))
  expect(Source.instances).toHaveLength(4)
  expect(host.textContent).toBe('error')
})

it('merges live calls into the rendered list and defers insertion while reading', async () => {
  const old = networkFixture()
  const fetcher = vi.fn(async (_input: RequestInfo | URL) => new Response(JSON.stringify({ items: [old], next_cursor: null }), { status: 200 }))
  vi.stubGlobal('fetch', fetcher)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  await act(async () => root.render(<QueryClientProvider client={client}><MemoryRouter>
    <CallExplorer caption="调用" listPath="/api/calls" streamPath="/api/calls/stream" params={{}} columns={summaryColumns()} />
  </MemoryRouter></QueryClientProvider>))
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  await act(async () => host.querySelector<HTMLButtonElement>('[role="switch"]')!.click())
  const source = Source.instances.at(-1)!
  const row = { ...old, id: 'new', model_id: '新到模型' }
  await act(async () => source.send('call.started', row))
  expect(host.querySelector('tbody')!.textContent).toContain('新到模型')
  await act(async () => {
    Object.defineProperty(window, 'scrollY', { configurable: true, value: 500 })
    window.dispatchEvent(new Event('scroll'))
  })
  await act(async () => source.send('call.finished', { ...row, id: 'pending', model_id: '暂缓插入模型' }))
  expect(host.querySelector('tbody')!.textContent).not.toContain('暂缓插入模型')
  expect(host.textContent).toContain('1 条新调用')
  const show = [...host.querySelectorAll('button')].find(button => button.textContent === '显示')!
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  await act(async () => show.click())
  expect(host.querySelector('tbody')!.textContent).toContain('暂缓插入模型')
  expect(fetcher.mock.calls.some(([url]) => String(url).includes('/analysis'))).toBe(false)
})
