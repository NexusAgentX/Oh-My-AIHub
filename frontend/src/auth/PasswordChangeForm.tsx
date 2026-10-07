import { useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import type { Account } from '../api/contracts'
import { Button, InlineError, PasswordField } from '../ui'
import { passwordProblem, passwordRuleText } from './credentialsRules'
import { useChangePasswordMutation } from './queries'

/** 首次改密与账户设置共用的改密表单：校验、错误展示与提交状态一致。 */
export function PasswordChangeForm({
  currentLabel = '当前密码',
  submitLabel,
  onChanged,
}: {
  currentLabel?: string
  submitLabel: string
  onChanged: (account: Account) => void
}) {
  const mutation = useChangePasswordMutation()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [error, setError] = useState('')

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (passwordProblem(newPassword)) {
      setError(`新密码不符合规则：${passwordRuleText}`)
      return
    }
    if (newPassword !== confirmation) {
      setError('两次输入的新密码不一致')
      return
    }
    setError('')
    try {
      const account = await mutation.mutateAsync({ currentPassword, newPassword })
      setCurrentPassword('')
      setNewPassword('')
      setConfirmation('')
      onChanged(account)
    } catch (caught) {
      setError(errorMessage(caught, '保存失败，请稍后重试'))
    }
  }

  return (
    <form className="identity-form" onSubmit={submit}>
      <InlineError>{error}</InlineError>
      <PasswordField
        autoComplete="current-password"
        label={currentLabel}
        onChange={(event) => setCurrentPassword(event.target.value)}
        required
        value={currentPassword}
      />
      <PasswordField
        autoComplete="new-password"
        hint={passwordRuleText}
        label="新密码"
        onChange={(event) => setNewPassword(event.target.value)}
        required
        value={newPassword}
      />
      <PasswordField
        autoComplete="new-password"
        label="确认新密码"
        onChange={(event) => setConfirmation(event.target.value)}
        required
        value={confirmation}
      />
      <Button
        disabled={!currentPassword || !newPassword || !confirmation}
        loading={mutation.isPending}
        type="submit"
      >
        {submitLabel}
      </Button>
    </form>
  )
}
