/**
 * 需要原因与二次确认的操作（调账、坏账核销、仲裁、强制下架、重置密码）的步骤状态。
 * edit：填写原因与参数；confirm：复述将要发生的事并再次确认；只有 confirm 步骤才会提交。
 */
export type ConfirmStep = 'edit' | 'confirm'

export type ConfirmState = { step: ConfirmStep; error: string }

export const initialConfirmState: ConfirmState = { step: 'edit', error: '' }

/**
 * 从 edit 前进到 confirm：requireReason 时原因必填；validate 返回错误文案时停留在 edit。
 * 已在 confirm 时保持不变（提交由调用方执行）。
 */
export function advanceConfirm(
  state: ConfirmState,
  input: { reason: string; requireReason: boolean; validate?: () => string },
): ConfirmState {
  if (state.step === 'confirm') return state
  if (input.requireReason && !input.reason.trim()) return { step: 'edit', error: '请填写原因' }
  const error = input.validate?.() ?? ''
  if (error) return { step: 'edit', error }
  return { step: 'confirm', error: '' }
}

/** 一次性凭据：reveal 后可见，dismiss 后清空且不可再取回。 */
export type SecretReveal = { title: string; username: string; password: string }

export type SecretAction = { type: 'reveal'; secret: SecretReveal } | { type: 'dismiss' }

export function secretReducer(_state: SecretReveal | null, action: SecretAction): SecretReveal | null {
  return action.type === 'reveal' ? action.secret : null
}
