// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { afterEach, beforeEach, expect, it } from 'vitest'
import { CommittedInput } from '../ui/CommittedInput'
import { useQueryOverlay } from '../ui/urlState'
import { useCallFilters } from './urlFilters'

let host: HTMLDivElement, root: Root
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
})
afterEach(async () => { await act(async () => root.unmount()); host.remove() })

it('preserves false, zero and empty drill facets; overlay back restores the filtered list', async () => {
  function Probe() {
    const { params, update } = useCallFilters()
    const [call, setCall] = useQueryOverlay('call')
    return <><output>{JSON.stringify(params)}</output><span>{call}</span>
      <button onClick={() => setCall('call-a')}>打开</button><button onClick={() => setCall(null)}>关闭</button>
      <button onClick={() => update({ outcome: 'succeeded' })}>筛选</button></>
  }
  const router = createMemoryRouter([{ path: '/calls', element: <Probe /> }], {
    initialEntries: ['/calls?stream=false&min_tokens=0&dimension=tag&dimension_value=&account_id=user-a'],
  })
  await act(async () => root.render(<RouterProvider router={router} />))
  expect(JSON.parse(host.querySelector('output')!.textContent!)).toMatchObject({ stream: false, min_tokens: 0, dimension: 'tag', dimension_value: '', account_id: 'user-a' })
  expect(JSON.parse(host.querySelector('output')!.textContent!).to).toBeUndefined()
  await act(async () => host.querySelectorAll('button')[0].click())
  expect(router.state.location.search).toContain('call=call-a')
  await act(async () => router.navigate(-1))
  expect(router.state.location.search).not.toContain('call=')
  await act(async () => host.querySelectorAll('button')[2].click())
  expect(router.state.location.search).toContain('outcome=succeeded')
  expect(router.state.location.search).toContain('account_id=user-a')
  await act(async () => host.querySelectorAll('button')[0].click())
  await act(async () => host.querySelectorAll('button')[1].click())
  expect(router.state.location.search).toContain('outcome=succeeded')
})

it('closes a deep-linked overlay on its host instead of leaving the application', async () => {
  function Probe() { const [, setCall] = useQueryOverlay('call'); return <button onClick={() => setCall(null)}>关闭</button> }
  const router = createMemoryRouter([{ path: '/calls', element: <Probe /> }], { initialEntries: ['/calls?call=direct&tag=keep'] })
  await act(async () => root.render(<RouterProvider router={router} />))
  await act(async () => host.querySelector('button')!.click())
  expect(router.state.location.pathname + router.state.location.search).toBe('/calls?tag=keep')
})

it('keeps numeric edits local until Enter, and Escape restores the committed value', async () => {
  function Probe() { const [value, setValue] = useState(''); return <><CommittedInput value={value} onCommit={setValue} /><output>{value}</output></> }
  await act(async () => root.render(<Probe />))
  const input = host.querySelector('input')!
  const nativeSet = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
  await act(async () => { nativeSet.call(input, '1000'); input.dispatchEvent(new Event('input', { bubbles: true })) })
  expect(host.querySelector('output')!.textContent).toBe('')
  expect(host.textContent).toContain('待应用')
  await act(async () => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
  expect(host.querySelector('output')!.textContent).toBe('1000')
  await act(async () => { nativeSet.call(input, '9'); input.dispatchEvent(new Event('input', { bubbles: true })); input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })) })
  expect(input.value).toBe('1000')
})
