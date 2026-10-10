// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import initScript from '../../public/theme-init.js?raw'
import { readThemeMode, resolveTheme, setThemeMode, startTheme, themeStorageKey } from './theme'

let systemDark = false
let mediaListeners: Array<() => void> = []

function setSystemDark(value: boolean) {
  systemDark = value
  mediaListeners.forEach((listener) => listener())
}

beforeEach(() => {
  systemDark = false
  mediaListeners = []
  window.localStorage.clear()
  delete document.documentElement.dataset.theme
  window.matchMedia = ((query: string) => ({
    get matches() {
      return query === '(prefers-color-scheme: dark)' && systemDark
    },
    addEventListener: (_: string, listener: () => void) => mediaListeners.push(listener),
    removeEventListener: (_: string, listener: () => void) => {
      mediaListeners = mediaListeners.filter((item) => item !== listener)
    },
  })) as unknown as typeof window.matchMedia
})

let stop: (() => void) | undefined
afterEach(() => stop?.())

describe('theme', () => {
  it('defaults to following the system and ignores unknown stored values', () => {
    expect(readThemeMode()).toBe('system')
    window.localStorage.setItem(themeStorageKey, 'sepia')
    expect(readThemeMode()).toBe('system')
  })

  it('persists an explicit choice and clears it when following the system again', () => {
    stop = startTheme()
    setThemeMode('dark')
    expect(window.localStorage.getItem(themeStorageKey)).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
    setThemeMode('system')
    expect(window.localStorage.getItem(themeStorageKey)).toBeNull()
    expect(document.documentElement.dataset.theme).toBe('light')
  })

  it('follows system changes only while in system mode', () => {
    stop = startTheme()
    setSystemDark(true)
    expect(document.documentElement.dataset.theme).toBe('dark')
    setThemeMode('light')
    setSystemDark(false)
    setSystemDark(true)
    expect(document.documentElement.dataset.theme).toBe('light')
  })

  it.each([
    [null, false],
    [null, true],
    ['system', true],
    ['light', true],
    ['dark', false],
    ['sepia', true],
  ])('resolves stored %s (system dark: %s) the same way as the pre-paint script', (stored, dark) => {
    if (stored !== null) window.localStorage.setItem(themeStorageKey, stored)
    systemDark = dark
    window.eval(initScript)
    expect(document.documentElement.dataset.theme).toBe(resolveTheme(readThemeMode()))
  })
})
