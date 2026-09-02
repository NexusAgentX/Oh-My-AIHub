import { useState, type FormEvent } from 'react'
import { ApiError } from '../api/client'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, PasswordField, StatusBadge } from '../ui/FormControls'
import { useAuth } from './AuthProvider'

function formatDate(value: string | null) {
  if (!value) return '尚未修改'
  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
}

export function AccountSettingsPage() {
  const { account, changePassword } = useAuth()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  if (!account) return null

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setMessage('')
    if (newPassword.length < 12 || newPassword !== confirmation) {
      setError(
        newPassword.length < 12
          ? '新密码至少需要 12 个字符'
          : '两次输入的新密码不一致',
      )
      return
    }
    setError('')
    setSubmitting(true)
    try {
      await changePassword(currentPassword, newPassword)
      setCurrentPassword('')
      setNewPassword('')
      setConfirmation('')
      setMessage('密码已更新')
    } catch (caught) {
      setError(
        caught instanceof ApiError ? caught.message : '保存失败，请稍后重试',
      )
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AppShell>
      <header className="page-heading">
        <div>
          <h1>账户设置</h1>
        </div>
        <StatusBadge status={account.status} />
      </header>

      <div className="settings-grid">
        <section className="panel">
          <header className="panel-heading">
            <h2>账户信息</h2>
          </header>
          <dl className="detail-list">
            <div>
              <dt>显示名称</dt>
              <dd>{account.display_name}</dd>
            </div>
            <div>
              <dt>用户名</dt>
              <dd>@{account.username}</dd>
            </div>
            <div>
              <dt>账户类型</dt>
              <dd>{account.is_admin ? '管理员' : '普通账户'}</dd>
            </div>
            <div>
              <dt>上次改密</dt>
              <dd>{formatDate(account.password_changed_at)}</dd>
            </div>
          </dl>
        </section>

        <section className="panel">
          <header className="panel-heading">
            <h2>修改密码</h2>
          </header>
          <form className="stack-form" onSubmit={submit}>
            <InlineError>{error}</InlineError>
            {message && (
              <div aria-live="polite" className="success-message">
                {message}
              </div>
            )}
            <PasswordField
              autoComplete="current-password"
              label="当前密码"
              onChange={(event) => setCurrentPassword(event.target.value)}
              required
              value={currentPassword}
            />
            <div className="field-row">
              <PasswordField
                autoComplete="new-password"
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
            </div>
            <div className="form-actions">
              <Button disabled={submitting} type="submit">
                {submitting ? '正在保存' : '更新密码'}
              </Button>
            </div>
          </form>
        </section>
      </div>
    </AppShell>
  )
}
