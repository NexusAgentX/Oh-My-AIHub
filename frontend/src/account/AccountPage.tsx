import { useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'
import { PasswordChangeForm } from '../auth/PasswordChangeForm'
import { useUpdateDisplayNameMutation } from '../auth/queries'
import { Button, Card, InlineError, PageHeader, SuccessMessage, TextField } from '../ui'

/** 账户设置：修改显示名、查看用户名与角色、修改密码。 */
export function AccountPage() {
  const { account } = useAuth()
  const [message, setMessage] = useState('')
  if (!account) return null
  return (
    <>
      <PageHeader title="账户设置" />
      <div className="account-grid">
        <Card title="账户">
          <DisplayNameForm current={account.display_name} />
          <dl className="detail-list account-details">
            <div>
              <dt>用户名</dt>
              <dd className="mono">{account.username}</dd>
            </div>
            <div>
              <dt>角色</dt>
              <dd>{account.is_admin ? '管理员' : '用户'}</dd>
            </div>
          </dl>
        </Card>
        <Card title="修改密码">
          <SuccessMessage>{message}</SuccessMessage>
          <PasswordChangeForm onChanged={() => setMessage('密码已更新')} submitLabel="更新密码" />
        </Card>
      </div>
    </>
  )
}

function DisplayNameForm({ current }: { current: string }) {
  const mutation = useUpdateDisplayNameMutation()
  const [displayName, setDisplayName] = useState(current)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const trimmed = displayName.trim()

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!trimmed) {
      setError('请填写显示名')
      return
    }
    setError('')
    setMessage('')
    try {
      const account = await mutation.mutateAsync(trimmed)
      setDisplayName(account.display_name)
      setMessage('显示名已更新')
    } catch (caught) {
      setError(errorMessage(caught, '保存失败，请稍后重试'))
    }
  }

  return (
    <form className="identity-form" onSubmit={submit}>
      <SuccessMessage>{message}</SuccessMessage>
      <InlineError>{error}</InlineError>
      <TextField
        label="显示名"
        maxLength={64}
        onChange={(event) => setDisplayName(event.target.value)}
        required
        value={displayName}
      />
      <Button disabled={!trimmed || trimmed === current} loading={mutation.isPending} type="submit">
        保存显示名
      </Button>
    </form>
  )
}
