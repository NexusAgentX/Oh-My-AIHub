import { useState, type FormEvent } from 'react'
import { Navigate, useNavigate } from 'react-router-dom'
import { ApiError } from '../api/client'
import { AuthShell } from '../layouts/AuthShell'
import {
  Button,
  InlineError,
  LoadingState,
  PasswordField,
  TextField,
} from '../ui/FormControls'
import { useAuth } from './AuthProvider'
import { defaultDestination } from './routePolicy'

export function LoginPage() {
  const { account, loading, login, sessionError } = useAuth()
  const navigate = useNavigate()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  if (!loading && account) return <Navigate replace to={defaultDestination(account)} />
  if (loading) {
    return (
      <AuthShell>
        <LoadingState />
      </AuthShell>
    )
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setError('')
    setSubmitting(true)
    try {
      const current = await login(username, password)
      navigate(defaultDestination(current), { replace: true })
    } catch (caught) {
      setError(
        caught instanceof ApiError ? caught.message : '登录失败，请稍后重试',
      )
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AuthShell>
      <div className="auth-heading">
        <h1>登录</h1>
        <p>使用管理员交付的账户凭据</p>
      </div>
      <form className="auth-form" onSubmit={submit}>
        <InlineError>{error || sessionError}</InlineError>
        <TextField
          autoComplete="username"
          autoFocus
          label="用户名"
          onChange={(event) => setUsername(event.target.value)}
          required
          value={username}
        />
        <PasswordField
          autoComplete="current-password"
          hint="受邀账户的初始凭据或你修改后的密码"
          label="密码"
          onChange={(event) => setPassword(event.target.value)}
          required
          value={password}
        />
        <Button disabled={loading || submitting || !username || !password} type="submit">
          {submitting ? '正在登录' : '登录'}
        </Button>
      </form>
    </AuthShell>
  )
}
