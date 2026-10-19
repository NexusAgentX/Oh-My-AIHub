// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import { useDetectionJob, useDetectionJobs } from './queries'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); vi.useRealTimers() })
it.each([false, true])('polls only active visible detection %s and keeps the contribution filter', async list => {
  vi.useFakeTimers()
  let active = true, visible = true
  vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visible ? 'visible' : 'hidden')
  const fetch = vi.fn(async (_input: string) => new Response(JSON.stringify(list ? { items: [{ execution_active: active }], next_cursor: null } : { job: { execution_active: active } })))
  vi.stubGlobal('fetch', fetch)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } }), host = document.createElement('div'), root = createRoot(host)
  function Single() { useDetectionJob('task'); return null }
  function List() { useDetectionJobs('', true, 'draft'); return null }
  await act(async () => root.render(<QueryClientProvider client={client}>{list ? <List /> : <Single />}</QueryClientProvider>))
  const tick = async () => act(async () => { await vi.advanceTimersByTimeAsync(2100) })
  try {
    await tick(); expect(fetch.mock.calls.length).toBeGreaterThan(1)
    if (list) expect(fetch.mock.calls[0][0]).toContain('draft_id=draft')
    visible = false; await act(async () => document.dispatchEvent(new Event('visibilitychange')))
    const hiddenCount = fetch.mock.calls.length; await tick(); expect(fetch).toHaveBeenCalledTimes(hiddenCount)
    visible = true; await act(async () => document.dispatchEvent(new Event('visibilitychange'))); active = false
    await tick(); const terminalCount = fetch.mock.calls.length; await tick(); expect(fetch).toHaveBeenCalledTimes(terminalCount)
  } finally { await act(async () => root.unmount()); client.clear(); host.remove() }
})
