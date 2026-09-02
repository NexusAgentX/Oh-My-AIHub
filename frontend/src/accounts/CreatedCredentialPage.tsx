import { useState } from 'react'
import { Navigate, useNavigate } from 'react-router-dom'
import { AppShell } from '../layouts/AppShell'
import { Button } from '../ui/FormControls'
import { Icon } from '../ui/Icon'
import { useEphemeralCredential } from './EphemeralCredentialProvider'

export function CreatedCredentialPage() {
  const { credential, clearCredential } = useEphemeralCredential()
  const navigate = useNavigate()
  const [copyState, setCopyState] = useState('')

  if (!credential) return <Navigate replace to="/admin/accounts" />

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(
        `用户名：${credential.username}\n初始密码：${credential.initialPassword}`,
      )
      setCopyState('凭据已复制')
    } catch {
      setCopyState('复制失败，请手动复制')
    }
  }

  const leave = () => {
    clearCredential()
    navigate('/admin/accounts', { replace: true })
  }

  const createAnother = () => {
    clearCredential()
    navigate('/admin/accounts?create=1', { replace: true })
  }

  return (
    <AppShell admin>
      <section className="credential-page">
        <div className="credential-success-mark">
          <Icon name="check" size={26} />
        </div>
        <h1>账号已创建</h1>
        <p className="credential-notice">
          <strong>仅显示一次</strong>
          <span>首次登录必须修改密码</span>
        </p>
        <div className="credential-card">
          <div>
            <span>用户名</span>
            <strong>{credential.username}</strong>
          </div>
          <div>
            <span>初始密码</span>
            <strong className="credential-secret">{credential.initialPassword}</strong>
          </div>
        </div>
        <div aria-live="polite" className="credential-copy-status">
          {copyState}
        </div>
        <div className="credential-actions">
          <Button icon={<Icon name="copy" />} onClick={() => void copy()}>
            复制登录凭据
          </Button>
          <Button onClick={leave} variant="secondary">
            返回账户列表
          </Button>
          <Button onClick={createAnother} variant="quiet">
            再创建一个
          </Button>
        </div>
      </section>
    </AppShell>
  )
}
