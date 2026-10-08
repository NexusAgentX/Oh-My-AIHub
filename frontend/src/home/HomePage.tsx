import { useState } from 'react'
import { useAuth } from '../auth/AuthProvider'
import { PasswordChangeForm } from '../auth/PasswordChangeForm'
import { formatPointAmount } from '../money/format'
import { Card, PageHeader, QueryBoundary, SuccessMessage } from '../ui'
import { usePoints } from './queries'

/** 占位首页：产品重写期间只展示账户与积分概况，完整首页由 Feature D 实现。 */
export function HomePage() {
  const { account } = useAuth()
  const points = usePoints()
  const [message, setMessage] = useState('')
  if (!account) return null
  return (
    <>
      <PageHeader title={`你好，${account.display_name}`} />
      <div className="account-grid">
        <Card flush title="积分">
          <QueryBoundary query={points}>
            {(data) => (
              <dl className="detail-list">
                <div>
                  <dt>余额</dt>
                  <dd className="num">{formatPointAmount(data.balance)}</dd>
                </div>
                <div>
                  <dt>信用额度</dt>
                  <dd className="num">{formatPointAmount(data.credit_limit)}</dd>
                </div>
                <div>
                  <dt>可透支额度</dt>
                  <dd className="num">{formatPointAmount(data.available)}</dd>
                </div>
              </dl>
            )}
          </QueryBoundary>
        </Card>
        <Card title="修改密码">
          <SuccessMessage>{message}</SuccessMessage>
          <PasswordChangeForm onChanged={() => setMessage('密码已更新')} submitLabel="更新密码" />
        </Card>
      </div>
    </>
  )
}
