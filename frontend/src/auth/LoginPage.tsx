import { useState, type FormEvent } from 'react'
import { Navigate, useNavigate } from 'react-router-dom'
import { ApiError } from '../api/client'
import { errorMessage } from '../api/query'
import { AuthShell } from '../layouts/AuthShell'
import { Button, InlineError, LoadingState, Notice, PasswordField, TextField } from '../ui'
import { useAuth } from './AuthProvider'
import { useLoginMutation } from './queries'
import { defaultDestination } from './routePolicy'

export function LoginPage() {
  const { account, loading, sessionError } = useAuth()
  const navigate = useNavigate()
  const mutation = useLoginMutation()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')

  if (!loading && account) return <Navigate replace to={defaultDestination(account)} />
  if (loading) {
    return (
      <AuthShell>
        <LoadingState />
      </AuthShell>
    )
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    mutation.mutate(
      { username, password },
      { onSuccess: (current) => navigate(defaultDestination(current), { replace: true }) },
    )
  }

  const rateLimited = mutation.error instanceof ApiError && mutation.error.status === 429
  const message = mutation.isError
    ? errorMessage(mutation.error, '登录失败，请稍后重试')
    : sessionError

  return (
    <AuthShell description="使用管理员交付的账户凭据" title="登录">
      <form className="identity-form" onSubmit={submit}>
        {rateLimited ? <Notice>{message}</Notice> : <InlineError>{message}</InlineError>}
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
          label="密码"
          onChange={(event) => setPassword(event.target.value)}
          required
          value={password}
        />
        <Button disabled={!username || !password} loading={mutation.isPending} type="submit">
          登录
        </Button>
      </form>
    </AuthShell>
  )
}
