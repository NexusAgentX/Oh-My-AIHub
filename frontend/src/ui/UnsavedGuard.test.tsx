// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { createMemoryRouter, Outlet, RouterProvider } from 'react-router-dom'
import { expect, it } from 'vitest'
import { Drawer } from './Dialog'
import { UnsavedGuardProvider, useUnsavedGuard } from './UnsavedGuard'

it('keeps parent drafts across child routes, guards each draft, and releases nested scroll locks', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
  const host = document.createElement('div'), root = createRoot(host)
  document.body.append(host)
  function Parent() { useUnsavedGuard(true, { scope: '/keys/key' }); return <><p>父表单草稿</p><Outlet /></> }
  function Child() { useUnsavedGuard(true); return <Drawer open title="模型覆盖" onClose={() => void router.navigate('/keys/key')}>子表单草稿</Drawer> }
  const router = createMemoryRouter([{ element: <UnsavedGuardProvider><Outlet /></UnsavedGuardProvider>, children: [{ path: '/keys/key', element: <Parent />, children: [{ path: 'routing/model', element: <Child /> }] }, { path: '/other', element: <p>其它页面</p> }] }], { initialEntries: ['/keys/key'] })
  try {
    await act(async () => root.render(<RouterProvider router={router} />))
    await act(async () => { await router.navigate('/keys/key/routing/model') })
    expect(router.state.location.pathname).toBe('/keys/key/routing/model')
    expect(document.body.style.overflow).toBe('hidden')
    await act(async () => { await router.navigate('/keys/key') })
    expect(host.querySelectorAll('dialog[open]')).toHaveLength(2)
    const confirm = () => host.querySelector<HTMLButtonElement>('dialog.modal[open] .modal-actions button:last-child')!
    await act(async () => host.querySelector<HTMLButtonElement>('dialog.modal[open] .modal-actions button:first-child')!.click())
    expect(router.state.location.pathname).toBe('/keys/key/routing/model')
    await act(async () => { await router.navigate('/keys/key') })
    await act(async () => confirm().click())
    expect(router.state.location.pathname).toBe('/keys/key')
    expect(host.textContent).toContain('父表单草稿')
    expect(document.body.style.overflow).toBe('')
    await act(async () => { await router.navigate('/other') })
    expect(router.state.location.pathname).toBe('/keys/key')
    await act(async () => confirm().click())
    expect(router.state.location.pathname).toBe('/other')
  } finally { await act(async () => root.unmount()); host.remove(); router.dispose() }
})
