import type { WheelEvent } from 'react'

/** 聚焦的数字输入框会被滚轮改值；滚动时先失焦，让滚轮只滚动页面。 */
export function blurNumberInputOnWheel(event: Pick<WheelEvent<HTMLInputElement>, 'currentTarget'>) {
  const input = event.currentTarget
  if (input.type === 'number' && document.activeElement === input) input.blur()
}
