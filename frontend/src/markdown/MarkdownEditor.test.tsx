// @vitest-environment jsdom
import { act, StrictMode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MarkdownEditor } from './MarkdownEditor'

const state = vi.hoisted(() => ({ instances: [] as Array<{ destroy: ReturnType<typeof vi.fn>; insertValue: ReturnType<typeof vi.fn>; disabled: ReturnType<typeof vi.fn>; enable: ReturnType<typeof vi.fn>; options: IOptions }> }))
vi.mock('./runtime', () => ({ loadMarkdownRuntime: () => Promise.resolve() }))
vi.mock('./MarkdownContent', () => ({ MarkdownContent: ({ value }: { value: string }) => <div>{value}</div> }))
vi.mock('vditor', () => ({ default: class {
  options: IOptions
  vditor: { options: IOptions }
  value: string
  destroy = vi.fn()
  disabled = vi.fn()
  enable = vi.fn()
  insertValue = vi.fn((value: string) => { this.value += value; this.options.input?.(this.value) })
  constructor(host: HTMLElement, options: IOptions) {
    this.options = options; this.vditor = { options }; this.value = options.value || ''
    host.innerHTML = '<textarea class="vditor-sv"></textarea>'
    state.instances.push(this)
    queueMicrotask(() => options.after?.())
  }
  getValue() { return this.value }
  setValue(value: string) { this.value = value }
} }))

let host: HTMLDivElement
let root: Root
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  state.instances.length = 0
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
})
afterEach(async () => { await act(async () => root.unmount()); host.remove() })
const url = '/api/forum/attachments/11111111-1111-4111-8111-111111111111'
async function choose(file: File) {
  const input = host.querySelector('input[type=file]')!
  Object.defineProperty(input, 'files', { configurable: true, value: [file] })
  await act(async () => { input.dispatchEvent(new Event('change', { bubbles: true })) })
}

describe('MarkdownEditor lifecycle and upload boundary', () => {
  it('initializes once in StrictMode, disables cache and names the source textarea', async () => {
    await act(async () => root.render(<StrictMode><MarkdownEditor value="hello" onChange={vi.fn()} disabled /></StrictMode>))
    expect(state.instances).toHaveLength(1)
    expect(state.instances[0].options.cache?.enable).toBe(false)
    expect(state.instances[0].options.preview?.mode).toBe('editor')
    expect(state.instances[0].disabled).toHaveBeenCalled()
    expect(host.querySelector('textarea')?.getAttribute('aria-label')).toBe('正文')
    expect(host.querySelector('.markdown-editor-source')?.hasAttribute('inert')).toBe(true)
  })
  it('hides the preview by default and puts upload and preview at the end of the toolbar', async () => {
    await act(async () => root.render(<MarkdownEditor value="**hi**" onChange={vi.fn()} upload={vi.fn()} />))
    const items = state.instances[0].options.toolbar!.filter((item): item is IMenuItem => typeof item !== 'string')
    expect(items.map((item) => item.name)).toEqual(['oma-upload', 'oma-preview'])
    expect(host.querySelector('[aria-label="Markdown 预览"]')).toBeNull()
    await act(async () => items[1].click!(new Event('click'), {} as IVditor))
    expect(host.querySelector('[aria-label="Markdown 预览"]')?.textContent).toBe('**hi**')
    await act(async () => items[1].click!(new Event('click'), {} as IVditor))
    expect(host.querySelector('[aria-label="Markdown 预览"]')).toBeNull()
  })
  it('reports pending and success, inserts escaped Markdown, and allows same-file retries', async () => {
    let complete!: (result: { url: string; name: string; isImage: boolean }) => void
    const upload = vi.fn(() => new Promise<{ url: string; name: string; isImage: boolean }>(resolve => { complete = resolve }))
    const uploading = vi.fn(), change = vi.fn()
    await act(async () => root.render(<MarkdownEditor value="" onChange={change} upload={upload} onUploadingChange={uploading} />))
    await choose(new File(['x'], 'guide[1].txt'))
    expect(uploading).toHaveBeenLastCalledWith(true)
    expect(host.querySelector('[role=status]')?.textContent).toContain('正在上传')
    await act(async () => complete({ url, name: 'guide[1].txt', isImage: false }))
    expect(change).toHaveBeenLastCalledWith(`\n[guide\\[1\\].txt](${url})\n`)
    expect(uploading).toHaveBeenLastCalledWith(false)
    expect((host.querySelector('input[type=file]') as HTMLInputElement).value).toBe('')
  })
  it('shows an accessible failure and never inserts a failed upload', async () => {
    await act(async () => root.render(<MarkdownEditor value="" onChange={vi.fn()} upload={async () => { throw new Error('文件过大') }} />))
    await choose(new File(['x'], 'file.txt'))
    expect(host.querySelector('[role=alert]')?.textContent).toBe('文件过大')
    expect(state.instances[0].insertValue).not.toHaveBeenCalled()
  })
  it('does not insert or update upload state after unmount', async () => {
    let complete!: (result: { url: string; name: string; isImage: boolean }) => void
    const uploading = vi.fn(), change = vi.fn()
    await act(async () => root.render(<MarkdownEditor value="" onChange={change} onUploadingChange={uploading}
      upload={() => new Promise(resolve => { complete = resolve })} />))
    await choose(new File(['x'], 'file.txt'))
    await act(async () => root.render(<p>closed</p>))
    expect(uploading.mock.calls).toEqual([[true], [false]])
    await act(async () => complete({ url, name: 'file.txt', isImage: false }))
    expect(change).not.toHaveBeenCalled()
    expect(uploading.mock.calls).toEqual([[true], [false]])
    expect(state.instances[0].destroy).toHaveBeenCalledOnce()
  })
  it('uploads pasted images and suppresses clipboard HTML conversion', async () => {
    const upload = vi.fn(async () => ({ url, name: 'pasted.png', isImage: true }))
    await act(async () => root.render(<MarkdownEditor value="" onChange={vi.fn()} upload={upload} />))
    const file = new File(['image'], 'pasted.png', { type: 'image/png' })
    const event = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(event, 'clipboardData', { value: { files: [file], getData: () => '<b>clipboard</b>' } })
    await act(async () => { host.querySelector('textarea')!.dispatchEvent(event) })
    expect(event.defaultPrevented).toBe(true)
    expect(upload).toHaveBeenCalledWith(file)
    expect(state.instances[0].insertValue).toHaveBeenCalledWith(`\n![pasted.png](${url})\n`)
  })
})
