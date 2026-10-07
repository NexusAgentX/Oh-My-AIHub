import type { ReactNode } from 'react'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useEphemeralCredential } from '../accounts/EphemeralCredentialProvider'
import { errorMessage } from '../api/query'
import { formatPointAmount } from '../wallet/presentation'
import { Button, Card, InlineError, PageHeader, StatusBadge, SuccessMessage } from '../ui'
import { useAuth } from './AuthProvider'
import { PasswordChangeForm } from './PasswordChangeForm'

function formatDate(value: string | null) {
  if (!value) return '尚未修改'
  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
}

export function AccountSettingsPage() {
  const { account, logout } = useAuth()
  const { clearCredential } = useEphemeralCredential()
  const navigate = useNavigate()
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')

  if (!account) return null

  const signOut = async () => {
    setError('')
    try {
      await logout()
      clearCredential()
      navigate('/login', { replace: true })
    } catch (caught) {
      setError(errorMessage(caught, '退出失败，请重试'))
    }
  }

  const rows: Array<[string, ReactNode]> = [
    ['显示名称', account.display_name],
    ['用户名', `@${account.username}`],
    ['角色', account.is_admin ? '管理员' : '消费者 · 共享者'],
    ['账户状态', <StatusBadge key="status" status={account.status} />],
    ['信用额度', `${formatPointAmount(account.credit_limit)} 积分`],
    ['密码更新', formatDate(account.password_changed_at)],
  ]

  return (
    <>
      <PageHeader
        actions={
          <Button onClick={() => void signOut()} type="button" variant="secondary">
            退出登录
          </Button>
        }
        title="账户设置"
      />
      <InlineError>{error}</InlineError>
      <div className="account-grid">
        <Card flush title="账户信息">
          <dl className="detail-list">
            {rows.map(([label, value]) => (
              <div key={label}>
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
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
