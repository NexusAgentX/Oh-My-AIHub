import { useState, type FormEvent } from 'react'
import { Navigate, useNavigate } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import { AuthShell } from '../layouts/AuthShell'
import {
  Button,
  InlineError,
  LoadingState,
  PasswordField,
  TextField,
} from '../ui/FormControls'
import { useInstance } from './InstanceProvider'
import { defaultDestination } from './routePolicy'
import { useAuth } from './AuthProvider'
import { passwordProblem, passwordRuleText, usernameProblem, usernameRuleText } from './credentialsRules'

export function InstanceInitializePage() {
  const { ready, initialized, refresh } = useInstance()
  const { refresh: refreshSession } = useAuth()
  const navigate = useNavigate()
  const [username, setUsername] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  if (!ready) {
    return (
      <AuthShell>
        <LoadingState />
      </AuthShell>
    )
  }
  if (initialized) return <Navigate replace to="/" />

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setError('')
    if (usernameProblem(username)) {
      setError(`管理员用户名不符合规则：${usernameRuleText}`)
      return
    }
    if (passwordProblem(password)) {
      setError(`密码不符合规则：${passwordRuleText}`)
      return
    }
    if (password !== confirm) {
      setError('两次输入的密码不一致')
      return
    }
    setSubmitting(true)
    try {
      const result = await api.initializeInstance(username, displayName, password)
      await refresh()
      const account = await refreshSession()
      navigate(defaultDestination(account ?? result.account), { replace: true })
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '初始化失败，请稍后重试')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AuthShell>
      <div className="auth-heading">
        <h1>初始化实例</h1>
        <p>创建首个管理员账户，开始使用</p>
      </div>
      <form className="auth-form" onSubmit={submit}>
        <InlineError>{error}</InlineError>
        <TextField
          autoComplete="username"
          autoFocus
          hint={usernameRuleText}
          label="管理员用户名"
          onChange={(event) => setUsername(event.target.value)}
          required
          value={username}
        />
        <TextField
          label="显示名称"
          onChange={(event) => setDisplayName(event.target.value)}
          required
          value={displayName}
        />
        <PasswordField
          autoComplete="new-password"
          hint={passwordRuleText}
          label="密码"
          onChange={(event) => setPassword(event.target.value)}
          required
          value={password}
        />
        <PasswordField
          autoComplete="new-password"
          label="确认密码"
          onChange={(event) => setConfirm(event.target.value)}
          required
          value={confirm}
        />
        <Button
          disabled={submitting || !username || !displayName || !password || !confirm}
          type="submit"
        >
          {submitting ? '正在创建' : '创建管理员并开始'}
        </Button>
      </form>
    </AuthShell>
  )
}
