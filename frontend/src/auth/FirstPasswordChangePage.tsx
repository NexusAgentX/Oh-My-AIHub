import { useState, type FormEvent } from 'react'
import { Navigate, useNavigate } from 'react-router-dom'
import { ApiError } from '../api/client'
import { AuthShell } from '../layouts/AuthShell'
import { Button, InlineError, PasswordField } from '../ui/FormControls'
import { useAuth } from './AuthProvider'
import { defaultDestination } from './routePolicy'

export function FirstPasswordChangePage() {
  const { account, changePassword } = useAuth()
  const navigate = useNavigate()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  if (account && !account.must_change_password) {
    return <Navigate replace to={defaultDestination(account)} />
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (newPassword.length < 12) {
      setError('新密码至少需要 12 个字符')
      return
    }
    if (newPassword !== confirmation) {
      setError('两次输入的新密码不一致')
      return
    }
    setError('')
    setSubmitting(true)
    try {
      const current = await changePassword(currentPassword, newPassword)
      navigate(defaultDestination(current), { replace: true })
    } catch (caught) {
      setError(
        caught instanceof ApiError ? caught.message : '保存失败，请稍后重试',
      )
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AuthShell>
      <div className="auth-heading">
        <span className="step-badge">首次登录</span>
        <h1>设置你的密码</h1>
      </div>
      <form className="auth-form" onSubmit={submit}>
        <InlineError>{error}</InlineError>
        <PasswordField
          autoComplete="current-password"
          autoFocus
          label="初始密码"
          onChange={(event) => setCurrentPassword(event.target.value)}
          required
          value={currentPassword}
        />
        <PasswordField
          autoComplete="new-password"
          hint="至少 12 个字符"
          label="新密码"
          minLength={12}
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
          disabled={
            submitting || !currentPassword || !newPassword || !confirmation
          }
          type="submit"
        >
          {submitting ? '正在保存' : '保存并进入'}
        </Button>
      </form>
    </AuthShell>
  )
}
