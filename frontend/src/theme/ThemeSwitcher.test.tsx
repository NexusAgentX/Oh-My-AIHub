// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { themeStorageKey } from './theme'
import { ThemeToggle } from './ThemeSwitcher'

let host: HTMLDivElement
let root: Root
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.localStorage.clear()
  window.matchMedia = ((query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

describe('ThemeToggle', () => {
  it('opens a three-way menu, applies the choice and closes on Escape', async () => {
    await act(async () => root.render(<ThemeToggle />))
    const button = host.querySelector<HTMLButtonElement>('button[aria-haspopup=menu]')!
    expect(button.getAttribute('aria-label')).toBe('主题：跟随系统')
    await act(async () => button.click())
    const items = [...host.querySelectorAll<HTMLButtonElement>('[role=menuitemradio]')]
    expect(items.map((item) => item.textContent)).toEqual(['跟随系统', '浅色', '深色'])
    await act(async () => items[2].click())
    expect(window.localStorage.getItem(themeStorageKey)).toBe('dark')
    expect(host.querySelector('[role=menu]')).toBeNull()
    expect(button.getAttribute('aria-label')).toBe('主题：深色')
    await act(async () => button.click())
    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })))
    expect(host.querySelector('[role=menu]')).toBeNull()
    expect(document.activeElement).toBe(button)
  })
})
