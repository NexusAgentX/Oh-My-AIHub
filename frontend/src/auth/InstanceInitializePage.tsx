import { useState, type FormEvent } from 'react'
import { Link, Navigate } from 'react-router-dom'
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

export function InstanceInitializePage() {
  const { ready, initialized, refresh } = useInstance()
  const [username, setUsername] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [created, setCreated] = useState(false)
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
    if (password !== confirm) {
      setError('两次输入的密码不一致')
      return
    }
    setSubmitting(true)
    try {
      await api.initializeInstance(username, displayName, password)
      await refresh()
      setCreated(true)
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '初始化失败，请稍后重试')
    } finally {
      setSubmitting(false)
    }
  }

  if (created) {
    return (
      <AuthShell>
        <div className="auth-heading">
          <h1>实例已就绪</h1>
          <p>首个管理员已创建，首次登录会要求修改密码</p>
        </div>
        <form className="auth-form">
          <InlineError>{error}</InlineError>
          <Link className="button" to="/login">
            前往登录
          </Link>
        </form>
      </AuthShell>
    )
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
