// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import type { RoutingPreference } from '../api/types'
import { useCreateKey } from './queries'
import { clearKeySecret, handoffKeySecret, takeKeySecret } from './secret'
import { KeyRoutingSection } from './KeyRouting'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { vi.restoreAllMocks(); clearKeySecret() })
const flush = () => new Promise(resolve => setTimeout(resolve, 5))
async function mount(element: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } })
  const host = document.createElement('div'), root = createRoot(host); document.body.append(host)
  await act(async () => root.render(<MemoryRouter><QueryClientProvider client={client}>{element}</QueryClientProvider></MemoryRouter>))
  return { host, client, close: async () => { await act(async () => root.unmount()); host.remove(); client.clear() } }
}
it('hands the creation secret to the detail once without retaining it in mutation data', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ key: { id: 'key' }, secret: 'synthetic-key-secret' }), { status: 201 }))
  function Create() { const create = useCreateKey(); return <button onClick={() => create.mutate({ name: 'Test', codex_notes: false })}>创建</button> }
  const { host, client, close } = await mount(<Create />)
  try {
    await act(async () => { host.querySelector('button')!.click(); await flush() })
    expect(JSON.stringify(client.getMutationCache().getAll().map(mutation => mutation.state.data))).not.toContain('synthetic-key-secret')
    expect(takeKeySecret('key')).toBe('synthetic-key-secret')
    expect(takeKeySecret('key')).toBe('')
    handoffKeySecret('key', 'other-secret'); clearKeySecret(); expect(takeKeySecret('key')).toBe('')
  } finally { await close() }
})
it('reports partial restore failures and retries only the failed model', async () => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
  const deleted: string[] = []; let fail = true
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, options) => {
    const path = String(input)
    if (options?.method === 'DELETE') {
      const model = path.split('/').at(-1)!; deleted.push(model)
      if (model === 'second' && fail) return new Response(JSON.stringify({ code: 'unavailable', message: '稍后重试' }), { status: 503 })
      return new Response(null, { status: 204 })
    }
    return new Response(JSON.stringify({ items: [], next_cursor: null }), { status: 200 })
  })
  const routing = ['first', 'second'].map(model_id => ({ model_id, mode: 'cheapest' })) as RoutingPreference[]
  const { host, close } = await mount(<KeyRoutingSection keyId="key" routing={routing} />)
  const button = (text: string) => [...host.querySelectorAll('button')].find(button => button.textContent === text)!
  try {
    await act(async () => button('全部恢复跟随账号').click())
    await act(async () => { button('恢复跟随账号').click(); await flush() })
    expect(host.textContent).toContain('已移除 1/2')
    expect(host.textContent).toContain('second：稍后重试')
    fail = false
    await act(async () => { button('重试失败的 1 项').click(); await flush() })
    expect(deleted).toEqual(['first', 'second', 'second'])
    expect(host.textContent).toContain('已移除 2/2')
  } finally { await close() }
})
