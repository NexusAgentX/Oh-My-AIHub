import { useState, type FormEvent } from 'react'
import { Navigate, useNavigate } from 'react-router-dom'
import { errorMessage } from '../api/query'
import { AuthShell } from '../layouts/AuthShell'
import { Button, InlineError, LoadingState, PasswordField, TextField } from '../ui'
import { passwordProblem, passwordRuleText, usernameProblem, usernameRuleText } from './credentialsRules'
import { useInstance } from './InstanceProvider'
import { useInitializeInstanceMutation } from './queries'
import { defaultDestination } from './routePolicy'

export function InstanceInitializePage() {
  const { ready, initialized } = useInstance()
  const navigate = useNavigate()
  const mutation = useInitializeInstanceMutation()
  const [username, setUsername] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')

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
    const problem = usernameProblem(username)
      ? `管理员用户名不符合规则：${usernameRuleText}`
      : passwordProblem(password)
        ? `密码不符合规则：${passwordRuleText}`
        : password !== confirm
          ? '两次输入的密码不一致'
          : ''
    setError(problem)
    if (problem) return
    try {
      // mutateAsync：实例状态刷新会卸载本页，mutate 的回调届时不再执行
      const account = await mutation.mutateAsync({ username, displayName, password })
      navigate(defaultDestination(account), { replace: true })
    } catch (caught) {
      setError(errorMessage(caught, '初始化失败，请稍后重试'))
    }
  }

  return (
    <AuthShell description="创建首个管理员账户" title="初始化实例">
      <form className="identity-form" onSubmit={submit}>
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
          disabled={!username || !displayName || !password || !confirm}
          loading={mutation.isPending}
          type="submit"
        >
          创建管理员并开始
        </Button>
      </form>
    </AuthShell>
  )
}
