// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { PageHeader } from './Card'
import { TopbarSlot } from './TopbarSlot'

let host: HTMLDivElement
let root: Root
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

describe('PageHeader in the topbar', () => {
  it('renders the parent link into the frame topbar instead of above the title', async () => {
    const slot = document.createElement('div')
    await act(async () => root.render(
      <MemoryRouter>
        <TopbarSlot.Provider value={slot}>
          <PageHeader back={{ to: '/forum', label: '论坛' }} title="帖子" />
        </TopbarSlot.Provider>
      </MemoryRouter>,
    ))
    const link = slot.querySelector('a')
    expect(link?.getAttribute('href')).toBe('/forum')
    expect(link?.getAttribute('aria-label')).toBe('返回论坛')
    expect(link?.textContent).toBe('论坛')
    expect(host.querySelector('.page-heading a')).toBeNull()
  })

  it('keeps the link above the title when there is no frame', async () => {
    await act(async () => root.render(
      <MemoryRouter>
        <PageHeader back={{ to: '/models', label: '模型' }} title="gpt-5" />
      </MemoryRouter>,
    ))
    expect(host.querySelector('.page-heading a')?.getAttribute('href')).toBe('/models')
  })

  it('shows the title in the topbar only after the heading scrolls under it', async () => {
    let notify!: (entries: Array<Partial<IntersectionObserverEntry>>) => void
    vi.stubGlobal('IntersectionObserver', class {
      constructor(callback: (entries: Array<Partial<IntersectionObserverEntry>>) => void) { notify = callback }
      observe() {}
      disconnect() {}
    })
    const slot = document.createElement('div')
    await act(async () => root.render(
      <MemoryRouter>
        <TopbarSlot.Provider value={slot}>
          <PageHeader back={{ to: '/forum', label: '论坛' }} title="帖子标题" />
        </TopbarSlot.Provider>
      </MemoryRouter>,
    ))
    expect(slot.querySelector('.topbar-title')).toBeNull()
    await act(async () => notify([{ isIntersecting: false, boundingClientRect: { top: -40 } as DOMRectReadOnly }]))
    expect(slot.querySelector('.topbar-title')?.textContent).toBe('/帖子标题')
    expect(slot.querySelector('.topbar-title')?.getAttribute('aria-hidden')).toBe('true')
    await act(async () => notify([{ isIntersecting: true, boundingClientRect: { top: 80 } as DOMRectReadOnly }]))
    expect(slot.querySelector('.topbar-title')).toBeNull()
    vi.unstubAllGlobals()
  })
})
