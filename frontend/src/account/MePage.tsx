import { Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { useSignOut } from '../layouts/AppFrame'
import { amountSign, formatPoints } from '../money/format'
import { usePoints } from '../points/queries'
import { ThemeSegmented } from '../theme/ThemeSwitcher'
import { Icon, InlineError, type IconName } from '../ui'

function MeLink({ to, icon, label }: { to: string; icon: IconName; label: string }) {
  return (
    <Link className="me-link" to={to}>
      <Icon name={icon} />
      <span>{label}</span>
      <Icon name="chevron-right" />
    </Link>
  )
}

/** 移动端「我的」：API Key、用量、账户设置、管理后台入口、主题与退出。 */
export function MePage() {
  const { account } = useAuth()
  const points = usePoints()
  const { signOut, error } = useSignOut()
  if (!account) return null
  return (
    <div className="me-page">
      <section className="me-profile">
        <span aria-hidden="true" className="avatar avatar-lg">
          {account.display_name.slice(0, 1) || '用'}
        </span>
        <div>
          <h1>{account.display_name}</h1>
          <p className="muted-copy">@{account.username}</p>
        </div>
        <Link className="me-balance" to="/points">
          <span>余额</span>
          <strong className={`num ${points.data && amountSign(points.data.balance) < 0 ? 'amount-negative' : ''}`}>
            {points.data ? formatPoints(points.data.balance, { digits: 2 }) : '—'}
          </strong>
        </Link>
      </section>
      <nav aria-label="我的" className="me-links">
        <MeLink icon="key" label="API Key" to="/keys" />
        <MeLink icon="chart" label="用量" to="/usage" />
        <MeLink icon="message" label="论坛" to="/forum" />
        <MeLink icon="ticket" label={account.is_admin ? '全部工单' : '我的工单'} to="/forum/tickets" />
        <MeLink icon="settings" label="账户设置" to="/account" />
        {account.is_admin && <MeLink icon="shield" label="管理后台" to="/admin" />}
      </nav>
      <section aria-label="主题" className="me-theme">
        <span>主题</span>
        <ThemeSegmented />
      </section>
      <InlineError>{error}</InlineError>
      <button className="me-link me-signout" onClick={() => void signOut()} type="button">
        <Icon name="logout" />
        <span>退出</span>
      </button>
    </div>
  )
}
