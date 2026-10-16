// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import NetworkReplay from './NetworkReplay'
import { networkFixture } from './network.fixture'
let host: HTMLDivElement, root: Root
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  vi.stubGlobal('matchMedia', () => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() }))
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
})
afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals() })
it('keeps reduced motion manual, switches attempts, and exposes measured regions and uncertainty', async () => {
  await act(async () => root.render(<NetworkReplay call={networkFixture()} />))
  expect(host.textContent).toContain('示例 · 东京地区')
  expect(host.textContent).toContain('连接复用')
  expect(host.textContent).toContain('到网关的网络耗时未测量')
  expect([...host.querySelectorAll('button')].find(b => b.textContent === '播放')?.disabled).toBe(true)
  const attempt = host.querySelector<HTMLSelectElement>('[aria-label="选择上游尝试"]')!
  await act(async () => { attempt.value = '0'; attempt.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(host.textContent).toContain('未建立连接')
  expect(host.textContent).not.toContain('连接复用')
  expect(host.querySelectorAll('.network-timeline li')).toHaveLength(3)
})
it('does not fabricate a map for old or unfinished calls', async () => {
  const call = networkFixture(); call.diagnostics!.network = null
  await act(async () => root.render(<NetworkReplay call={call} />))
  expect(host.textContent).toContain('未采集')
  expect(host.querySelector('svg')).toBeNull()
  call.diagnostics = networkFixture().diagnostics; call.completed_at = null
  await act(async () => root.render(<NetworkReplay call={call} />))
  expect(host.textContent).toContain('请求完成后')
})
