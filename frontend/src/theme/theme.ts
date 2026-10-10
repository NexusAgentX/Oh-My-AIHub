import { useSyncExternalStore } from 'react'

/** 主题模式：跟随系统、浅色、深色（墨夜）。偏好只存在本机，不随账户同步。 */
export type ThemeMode = 'system' | 'light' | 'dark'
export type Theme = 'light' | 'dark'

/** 与 public/theme-init.js 一致：首屏前按同一键名与规则设置 data-theme。 */
export const themeStorageKey = 'oma-theme'
const darkQuery = '(prefers-color-scheme: dark)'

export const themeOptions: Array<{ key: ThemeMode; label: string }> = [
  { key: 'system', label: '跟随系统' },
  { key: 'light', label: '浅色' },
  { key: 'dark', label: '深色' },
]

function isThemeMode(value: unknown): value is ThemeMode {
  return value === 'system' || value === 'light' || value === 'dark'
}

export function readThemeMode(): ThemeMode {
  try {
    const stored = window.localStorage.getItem(themeStorageKey)
    return isThemeMode(stored) ? stored : 'system'
  } catch {
    return 'system'
  }
}

function systemPrefersDark() {
  return typeof window.matchMedia === 'function' && window.matchMedia(darkQuery).matches
}

export function resolveTheme(mode: ThemeMode): Theme {
  if (mode === 'system') return systemPrefersDark() ? 'dark' : 'light'
  return mode
}

/** 设置 data-theme，并让手机浏览器地址栏跟随纸面颜色。 */
function applyTheme(mode: ThemeMode) {
  const root = document.documentElement
  root.dataset.theme = resolveTheme(mode)
  const paper = getComputedStyle(root).getPropertyValue('--paper').trim()
  if (!paper) return
  let meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
  if (!meta) {
    meta = document.createElement('meta')
    meta.name = 'theme-color'
    document.head.append(meta)
  }
  meta.content = paper
}

let current: ThemeMode = 'system'
const listeners = new Set<() => void>()

function update(mode: ThemeMode) {
  current = mode
  applyTheme(mode)
  listeners.forEach((listener) => listener())
}

export function setThemeMode(mode: ThemeMode) {
  try {
    if (mode === 'system') window.localStorage.removeItem(themeStorageKey)
    else window.localStorage.setItem(themeStorageKey, mode)
  } catch {
    // 无法写入本机存储（隐私模式等）时只在本页生效
  }
  update(mode)
}

/** 应用启动时调用一次：应用保存的偏好，并跟随系统切换与其他标签页的修改。返回清理函数。 */
export function startTheme() {
  update(readThemeMode())
  const media = typeof window.matchMedia === 'function' ? window.matchMedia(darkQuery) : null
  const onSystemChange = () => {
    if (current === 'system') applyTheme(current)
  }
  const onStorage = (event: StorageEvent) => {
    if (event.key === null || event.key === themeStorageKey) update(readThemeMode())
  }
  media?.addEventListener('change', onSystemChange)
  window.addEventListener('storage', onStorage)
  return () => {
    media?.removeEventListener('change', onSystemChange)
    window.removeEventListener('storage', onStorage)
  }
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function useThemeMode(): [ThemeMode, (mode: ThemeMode) => void] {
  const mode = useSyncExternalStore(subscribe, () => current, () => 'system' as ThemeMode)
  return [mode, setThemeMode]
}
