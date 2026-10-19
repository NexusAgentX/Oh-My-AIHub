// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { CallAnalysis } from './CallAnalysis'
const values = { calls: 2, finished: 2, succeeded: 1, charged: '0.026' }
vi.mock('@tanstack/react-query', () => ({ keepPreviousData: (value: unknown) => value, useQuery: () => ({ data: {
  from: '2026-10-01T00:30:00Z', to: '2026-10-01T01:30:00Z', previous_from: '2026-09-30T23:30:00Z', previous_to: '2026-10-01T00:30:00Z',
  current: values, previous: values, bucket_seconds: 3600,
  trend: [{ key: '2026-10-01T00:00:00Z', values }, { key: '2026-10-01T01:00:00Z', values }],
  groups: [{ key: 'demo', label: 'demo', split_key: '', split_label: '', values }],
} }) }))
let host: HTMLDivElement, root: Root
beforeEach(() => { Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); host = document.createElement('div'); document.body.append(host); root = createRoot(host) })
afterEach(async () => { await act(async () => root.unmount()); host.remove() })
it('inspects points before drilling and keeps one arrow-navigable chart stop with clipped time bounds', async () => {
  const drill = vi.fn()
  await act(async () => root.render(<CallAnalysis path="/api/calls" params={{ from: '2026-10-01T00:30:00Z', to: '2026-10-01T01:30:00Z' }} live={false} onDrill={drill} />))
  const bars = host.querySelectorAll<HTMLButtonElement>('.analysis-trend button')
  await act(async () => bars[0].click())
  expect(drill).not.toHaveBeenCalled()
  expect(host.querySelector('output')?.textContent).toContain('请求数 2')
  await act(async () => { bars[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true })) })
  expect(document.activeElement).toBe(bars[1])
  expect([...bars].filter(b => b.tabIndex === 0)).toEqual([bars[1]])
  await act(async () => host.querySelector<HTMLButtonElement>('.analysis-reading button')!.click())
  expect(drill.mock.calls[0][0]).toEqual({ from: '2026-10-01T01:00:00.000Z', to: '2026-10-01T01:30:00.000Z' })
  expect(host.querySelector('.analysis-comparison-row')?.textContent).not.toContain('2 次')
  await act(async () => { [...host.querySelectorAll('button')].find(b => b.textContent === '费用')!.click() })
  const row = host.querySelector('.analysis-comparison-row')!
  expect(row.textContent).toContain('实际扣费0.026 积分')
  expect(row.textContent).toContain('请求2 次')
  expect(row.textContent).toContain('成功率50 %')
})
