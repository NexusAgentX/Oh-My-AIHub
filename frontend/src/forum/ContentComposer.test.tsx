// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MarkdownEditorProps } from '../markdown/MarkdownEditor'
import { ContentComposer } from './ContentComposer'
import { NewTicketPage } from './TopicEditorPage'
import { forumApi } from './queries'
import { ApiError } from '../api/client'
import { createMemoryRouter, RouterProvider, MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom'

const state = vi.hoisted(() => ({ editor: {} as MarkdownEditorProps, save: vi.fn() }))
vi.mock('../markdown', () => ({ MarkdownEditor: (props: MarkdownEditorProps) => { state.editor = props; return <textarea value={props.value} readOnly /> } }))
vi.mock('../auth/AuthProvider', () => ({ useAuth: () => ({ account: { id: 'owner' } }) }))
vi.mock('./queries', () => ({
  forumApi: { upload: vi.fn(), createTopic: vi.fn(), updateTopic: vi.fn() },
  useBoards: () => ({ data: { items: [] } }), useTopic: vi.fn(),
  useForumAction: () => ({ mutateAsync: state.save }),
}))
let host: HTMLDivElement, root: Root
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  vi.clearAllMocks()
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
})
afterEach(async () => { await act(async () => root.unmount()); host.remove() })
async function submit() { await act(async () => { host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) }) }
const file = { id: 'a', name: 'guide.txt', media_type: 'text/plain', size: 2, url: '/api/forum/attachments/a', inline: false }

describe('forum content submission', () => {
  it('keeps existing attachments and explicitly sends an empty list after removal', async () => {
    const save = vi.fn(async () => undefined)
    await act(async () => root.render(<ContentComposer initialBody="body" initialAttachments={[file]} onSave={save} submitLabel="Save" />))
    await submit()
    expect(save).toHaveBeenLastCalledWith('body', ['a'])
    await act(async () => { (host.querySelector('button[aria-label]') as HTMLButtonElement).click() })
    await submit()
    expect(save).toHaveBeenLastCalledWith('body', [])
  })
  it('blocks submission while uploading, then binds the returned file ID', async () => {
    const save = vi.fn(async () => undefined)
    vi.mocked(forumApi.upload).mockResolvedValue(file)
    await act(async () => root.render(<ContentComposer initialBody="body" onSave={save} submitLabel="Save" />))
    await act(async () => state.editor.onUploadingChange!(true))
    await submit(); expect(save).not.toHaveBeenCalled()
    await act(async () => { expect(await state.editor.upload!(new File(['ok'], 'guide.txt'))).toEqual({ url: file.url, name: file.name, isImage: false }); state.editor.onUploadingChange!(false) })
    await submit(); expect(save).toHaveBeenCalledWith('body', ['a'])
  })
  it('shows a server failure and retains the draft for retry', async () => {
    const save = vi.fn().mockRejectedValueOnce(new ApiError(409, 'quota', 'quota')).mockResolvedValue(undefined)
    await act(async () => root.render(<ContentComposer initialBody="keep me" onSave={save} submitLabel="Save" />))
    await submit(); expect(host.querySelector('[role=alert]')?.textContent).toBe('quota')
    expect(state.editor.value).toBe('keep me')
    await submit(); expect(save).toHaveBeenCalledTimes(2)
  })
  it('rejects oversize uploads before sending any file', async () => {
    await act(async () => root.render(<ContentComposer onSave={vi.fn()} submitLabel="Save" />))
    const large = new File(['x'], 'large.txt'); Object.defineProperty(large, 'size', { value: 10 * 1048576 + 1 })
    await expect(state.editor.upload!(large)).rejects.toThrow('10 MiB')
    expect(forumApi.upload).not.toHaveBeenCalled()
  })
  it('does not navigate back when an old save finishes after leaving the editor', async () => {
    let finish!: (value: { id: string }) => void
    state.save.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    function Leave() { const navigate = useNavigate(); return <button type="button" id="leave" onClick={() => navigate('/elsewhere')}>Leave</button> }
    await act(async () => root.render(<MemoryRouter initialEntries={['/new']}><Leave /><Routes><Route path="/new" element={<NewTicketPage />} /><Route path="/elsewhere" element={<p>Elsewhere</p>} /><Route path="/forum/topics/:id" element={<p>Old topic</p>} /></Routes></MemoryRouter>))
    const title = host.querySelector('input')!
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(title, 'Ticket'); title.dispatchEvent(new Event('input', { bubbles: true })); state.editor.onChange('body') })
    await submit(); expect(state.save).toHaveBeenCalledOnce()
    await act(async () => { host.querySelector<HTMLButtonElement>('#leave')!.click() })
    await act(async () => finish({ id: 'old' }))
    expect(host.textContent).toContain('Elsewhere'); expect(host.textContent).not.toContain('Old topic')
  })
})


describe('unsaved forum navigation', () => {
  it('blocks links and history back, keeps a cancelled draft and permits confirmed discard', async () => {
    const router = createMemoryRouter([
      { path: '/edit', element: <ContentComposer onSave={vi.fn()} submitLabel="Save" /> },
      { path: '/away', element: <p>Away</p> },
    ], { initialEntries: ['/away', '/edit'] })
    await act(async () => root.render(<RouterProvider router={router} />))
    await act(async () => state.editor.onChange('unsaved body'))
    await act(async () => { void router.navigate(-1) })
    expect(host.textContent).toContain('丢弃未保存的正文')
    await act(async () => { [...host.querySelectorAll('button')].find(b => b.textContent === '取消')!.click() })
    expect(state.editor.value).toBe('unsaved body')
    expect(router.state.location.pathname).toBe('/edit')
    await act(async () => { void router.navigate('/away') })
    await act(async () => { [...host.querySelectorAll('button')].find(b => b.textContent === '丢弃并离开')!.click() })
    expect(router.state.location.pathname).toBe('/away')
  })
  it('allows blank drafts and successful submit navigation without a discard dialog', async () => {
    let router: ReturnType<typeof createMemoryRouter>
    const save = vi.fn(async () => { await router.navigate('/away') })
    router = createMemoryRouter([
      { path: '/edit', element: <ContentComposer onSave={save} submitLabel="Save" /> },
      { path: '/away', element: <p>Away</p> },
    ], { initialEntries: ['/edit'] })
    await act(async () => root.render(<RouterProvider router={router} />))
    await act(async () => state.editor.onChange('   '))
    await act(async () => { await router.navigate('/away') })
    expect(router.state.location.pathname).toBe('/away')
    await act(async () => { await router.navigate('/edit') })
    await act(async () => state.editor.onChange('ready to submit'))
    await submit()
    expect(save).toHaveBeenCalledOnce()
    expect(router.state.location.pathname).toBe('/away')
  })
})
