import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { ConfirmActionDialog, OneTimeSecretDialog } from './components'
import { advanceConfirm, initialConfirmState, secretReducer, type SecretReveal } from './confirm'

describe('reason + second confirmation', () => {
  it('stays on the edit step until a reason is given', () => {
    expect(advanceConfirm(initialConfirmState, { reason: '  ', requireReason: true })).toEqual({
      step: 'edit',
      error: '请填写原因',
    })
    expect(advanceConfirm(initialConfirmState, { reason: '买家已提供付款截图', requireReason: true })).toEqual({
      step: 'confirm',
      error: '',
    })
  })

  it('runs extra validation before the confirm step', () => {
    const state = advanceConfirm(initialConfirmState, { reason: 'x', requireReason: true, validate: () => '金额无效' })
    expect(state).toEqual({ step: 'edit', error: '金额无效' })
  })

  it('still requires a confirm step when no reason is needed (password reset)', () => {
    expect(advanceConfirm(initialConfirmState, { reason: '', requireReason: false }).step).toBe('confirm')
  })

  it('renders the reason field and a next-step button instead of submitting directly', () => {
    const markup = renderToStaticMarkup(
      <ConfirmActionDialog
        confirmLabel="确认判给买家"
        onClose={() => undefined}
        onConfirm={async () => undefined}
        open
        summary={<p>托管积分转给买家</p>}
        title="判给买家"
      />,
    )
    expect(markup).toContain('原因')
    expect(markup).toContain('<textarea')
    expect(markup).toContain('下一步')
    expect(markup).not.toContain('确认判给买家')
  })
})

describe('one-time password', () => {
  const secret: SecretReveal = { title: '用户已创建', username: 'alice', password: 'Init-Pass-123' }

  it('is discarded on dismiss and cannot be shown again', () => {
    const revealed = secretReducer(null, { type: 'reveal', secret })
    expect(revealed).toEqual(secret)
    const dismissed = secretReducer(revealed, { type: 'dismiss' })
    expect(dismissed).toBeNull()
    expect(secretReducer(dismissed, { type: 'dismiss' })).toBeNull()
  })

  it('shows the password with copy and offline-delivery hint only while revealed', () => {
    const shown = renderToStaticMarkup(<OneTimeSecretDialog onDismiss={() => undefined} secret={secret} />)
    expect(shown).toContain('Init-Pass-123')
    expect(shown).toContain('只显示这一次')
    expect(shown).toContain('复制密码')
    expect(shown).toContain('线下')
    const hidden = renderToStaticMarkup(<OneTimeSecretDialog onDismiss={() => undefined} secret={null} />)
    expect(hidden).not.toContain('Init-Pass-123')
  })
})
