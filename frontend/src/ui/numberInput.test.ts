import { afterEach, describe, expect, it, vi } from 'vitest'
import { blurNumberInputOnWheel } from './numberInput'

function wheelTarget(type: string, focused: boolean) {
  const input = { type, blur: vi.fn() }
  vi.stubGlobal('document', { activeElement: focused ? input : null })
  return { input, event: { currentTarget: input as unknown as HTMLInputElement } }
}

describe('blurNumberInputOnWheel', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('blurs a focused number input so the wheel cannot change its value', () => {
    const { input, event } = wheelTarget('number', true)
    blurNumberInputOnWheel(event)
    expect(input.blur).toHaveBeenCalledOnce()
  })

  it('leaves unfocused number inputs and other input types alone', () => {
    const unfocused = wheelTarget('number', false)
    blurNumberInputOnWheel(unfocused.event)
    expect(unfocused.input.blur).not.toHaveBeenCalled()
    const text = wheelTarget('text', true)
    blurNumberInputOnWheel(text.event)
    expect(text.input.blur).not.toHaveBeenCalled()
  })
})
