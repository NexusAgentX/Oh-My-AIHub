// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { PageHeader } from './Card'
import { PageBackSlot } from './PageBack'

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

describe('PageHeader back', () => {
  it('renders the parent link into the frame topbar instead of above the title', async () => {
    const slot = document.createElement('div')
    await act(async () => root.render(
      <MemoryRouter>
        <PageBackSlot.Provider value={slot}>
          <PageHeader back={{ to: '/forum', label: '论坛' }} title="帖子" />
        </PageBackSlot.Provider>
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
})
