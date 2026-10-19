// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { expect, it } from 'vitest'
import { LegacyRedirect } from './LegacyRedirect'

it.each([
  ['/channels/abc?group=selected#config', '/channels', '/supply/channels', '/supply/channels/abc?group=selected#config'],
  ['/forum/tickets/new?from=home', '/forum/tickets', '/tickets', '/tickets/new?from=home'],
  ['/detection/reports/id', '/detection', '/supply/detection', '/supply/detection/reports/id'],
])('preserves object, filters and anchor when redirecting %s', async (source, from, to, expected) => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const host = document.createElement('div'); const root = createRoot(host)
  const router = createMemoryRouter([{ path: from + '/*', element: <LegacyRedirect from={from} to={to} /> }, { path: to + '/*', element: <p>目标</p> }], { initialEntries: [source] })
  await act(async () => root.render(<RouterProvider router={router} />))
  const location = router.state.location
  expect(location.pathname + location.search + location.hash).toBe(expected)
  await act(async () => root.unmount())
})
