// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { SuggestInput } from './Suggest'

const presets = [
  { value: 'openai:default', label: 'OpenAI 标准' },
  { value: 'openai:priority', label: 'OpenAI 优先' },
  { value: 'anthropic:priority', label: 'Anthropic 优先' },
]

let host: HTMLDivElement
let root: Root
let changes: string[] = []
const current = () => changes.at(-1) ?? ''
function Harness() {
  const [value, setValue] = useState('')
  return (
    <SuggestInput
      aria-label="档位"
      onChange={(event) => {
        changes.push(event.target.value)
        setValue(event.target.value)
      }}
      suggestions={presets}
      value={value}
    />
  )
}
const input = () => host.querySelector<HTMLInputElement>('input')!
const options = () => [...host.querySelectorAll('[role=option]')].map((option) => option.querySelector('.suggest-value')?.textContent)
const key = (name: string) => act(async () => { input().dispatchEvent(new KeyboardEvent('keydown', { key: name, bubbles: true, cancelable: true })) })
async function type(text: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input(), text)
    input().dispatchEvent(new Event('input', { bubbles: true }))
  })
}

beforeEach(async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  changes = []
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => root.render(<Harness />))
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

describe('SuggestInput', () => {
  it('is a combobox that opens all suggestions on click and filters while typing', async () => {
    expect(input().getAttribute('role')).toBe('combobox')
    expect(input().getAttribute('aria-expanded')).toBe('false')
    await act(async () => input().click())
    expect(options()).toEqual(['openai:default', 'openai:priority', 'anthropic:priority'])
    expect(input().getAttribute('aria-expanded')).toBe('true')
    await type('优先')
    expect(options()).toEqual(['openai:priority', 'anthropic:priority'])
    await type('custom:tier')
    expect(host.querySelector('[role=listbox]')).toBeNull()
    expect(current()).toBe('custom:tier')
  })

  it('moves with the arrow keys and fills the value on Enter', async () => {
    await key('ArrowDown')
    expect(input().getAttribute('aria-activedescendant')).toBe(host.querySelectorAll('[role=option]')[0].id)
    await key('ArrowDown')
    await key('Enter')
    expect(current()).toBe('openai:priority')
    expect(host.querySelector('[role=listbox]')).toBeNull()
  })

  it('fills on click, and Escape closes the list before it closes a parent dialog', async () => {
    await act(async () => input().click())
    await act(async () => host.querySelectorAll('[role=option]')[2].dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true })))
    expect(current()).toBe('anthropic:priority')
    await act(async () => input().click())
    // 浏览器只在 Esc 的 keydown 没被阻止时才关闭 <dialog>
    const escape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })
    await act(async () => { input().dispatchEvent(escape) })
    expect(host.querySelector('[role=listbox]')).toBeNull()
    expect(escape.defaultPrevented).toBe(true)
    // 列表已收起时 Esc 照常交给外层（关闭抽屉）
    const second = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })
    await act(async () => { input().dispatchEvent(second) })
    expect(second.defaultPrevented).toBe(false)
  })
})
