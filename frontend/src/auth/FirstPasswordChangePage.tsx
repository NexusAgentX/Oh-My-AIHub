import { Navigate, useNavigate } from 'react-router-dom'
import { AuthShell } from '../layouts/AuthShell'
import { useAuth } from './AuthProvider'
import { PasswordChangeForm } from './PasswordChangeForm'
import { defaultDestination } from './routePolicy'

export function FirstPasswordChangePage() {
  const { account } = useAuth()
  const navigate = useNavigate()

  if (account && !account.must_change_password) {
    return <Navigate replace to={defaultDestination(account)} />
  }

  return (
    <AuthShell badge="首次登录" title="设置你的密码">
      <PasswordChangeForm
        currentLabel="初始密码"
        onChanged={(current) => navigate(defaultDestination(current), { replace: true })}
        submitLabel="保存并进入"
      />
    </AuthShell>
  )
}
