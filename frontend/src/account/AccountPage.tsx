import { useState } from 'react'
import { useAuth } from '../auth/AuthProvider'
import { PasswordChangeForm } from '../auth/PasswordChangeForm'
import { Card, PageHeader, SuccessMessage } from '../ui'

/** 账户设置：显示名（只读，由管理员修改）、用户名、修改密码。 */
export function AccountPage() {
  const { account } = useAuth()
  const [message, setMessage] = useState('')
  if (!account) return null
  return (
    <>
      <PageHeader title="账户设置" />
      <div className="account-grid">
        <Card flush title="账户">
          <dl className="detail-list">
            <div>
              <dt>显示名</dt>
              <dd>{account.display_name}</dd>
            </div>
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
