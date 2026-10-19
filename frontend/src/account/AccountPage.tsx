import { useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'
import { PasswordChangeForm } from '../auth/PasswordChangeForm'
import { useUpdateDisplayNameMutation } from '../auth/queries'
import { useSignOut } from '../layouts/AppFrame'
import { WorkspaceSwitcher } from '../layouts/WorkspaceSwitcher'
import { ThemeSegmented } from '../theme/ThemeSwitcher'
import { Button, ButtonLink, Card, InlineError, PageHeader, SuccessMessage } from '../ui'
import { useBack } from '../ui/urlState'

/** 账户设置：显示名（点击可改）、用户名、角色与修改密码。 */
export function AccountPage() {
  const { account } = useAuth()
  const [message, setMessage] = useState('')
  const { signOut, error } = useSignOut()
  const back = useBack('/home', '首页')
  if (!account) return null
  return (
    <>
      <PageHeader title="账户" back={back} />
      <div className="account-grid">
        <Card flush title="资料">
          <dl className="detail-list">
            <DisplayNameRow current={account.display_name} />
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
        <Card title="修改密码" className="account-password">
          <SuccessMessage>{message}</SuccessMessage>
          <PasswordChangeForm onChanged={() => setMessage('密码已更新')} submitLabel="更新密码" />
        </Card>
        <Card title="主题"><ThemeSegmented /></Card>
        <Card title="社区"><div className="account-links"><ButtonLink to="/forum">论坛</ButtonLink><ButtonLink to="/tickets">{account.is_admin ? '全部工单' : '我的工单'}</ButtonLink></div></Card>
        {account.is_admin && <Card><WorkspaceSwitcher /></Card>}
      </div>
      <InlineError>{error}</InlineError>
      <div className="form-actions"><Button variant="quiet" onClick={() => void signOut()}>退出登录</Button></div>
    </>
  )
}

/** 显示名默认只读展示，点击后原地编辑；Enter 保存，Esc 取消。 */
function DisplayNameRow({ current }: { current: string }) {
  const mutation = useUpdateDisplayNameMutation()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(current)
  const [error, setError] = useState('')
  const trimmed = draft.trim()

  const startEditing = () => {
    setDraft(current)
    setError('')
    setEditing(true)
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!trimmed) {
      setError('请填写显示名')
      return
    }
    if (trimmed === current) {
      setEditing(false)
      return
    }
    try {
      await mutation.mutateAsync(trimmed)
      setEditing(false)
    } catch (caught) {
      setError(errorMessage(caught, '保存失败，请稍后重试'))
    }
  }

  if (!editing) {
    return (
      <div>
        <dt>显示名</dt>
        <dd>
          <button className="inline-edit-trigger" onClick={startEditing} title="点击修改显示名" type="button">
            {current}
          </button>
        </dd>
      </div>
    )
  }

  return (
    <div className="inline-edit-row">
      <dt>显示名</dt>
      <dd>
        <form className="inline-edit-form" onSubmit={submit}>
          <input
            aria-invalid={Boolean(error)}
            aria-label="显示名"
            autoFocus
            className="input"
            maxLength={64}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Escape') setEditing(false)
            }}
            value={draft}
          />
          <Button loading={mutation.isPending} size="sm" type="submit">
            保存
          </Button>
          <Button disabled={mutation.isPending} onClick={() => setEditing(false)} size="sm" type="button" variant="quiet">
            取消
          </Button>
        </form>
        {error && <p className="field-message field-error">{error}</p>}
      </dd>
    </div>
  )
}
