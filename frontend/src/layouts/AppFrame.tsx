import { useEffect, useRef, useState } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useEphemeralCredential } from '../accounts/EphemeralCredentialProvider'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'
import { Drawer, Icon, IconButton } from '../ui'
import { formatPointAmount } from '../wallet/presentation'
import { useWallet } from '../wallet/queries'
import { Brand } from './Brand'
import { findNavItem, flattenNavigation, type NavGroup } from './navigation'

type Variant = 'product' | 'admin'

function useSignOut() {
  const { logout } = useAuth()
  const { clearCredential } = useEphemeralCredential()
  const navigate = useNavigate()
  const [error, setError] = useState('')
  const signOut = async () => {
    setError('')
    try {
      await logout()
      clearCredential()
      navigate('/login', { replace: true })
    } catch (caught) {
      setError(errorMessage(caught, '退出失败，请重试'))
    }
  }
  return { signOut, error }
}

/** 分组导航列表，侧边栏与移动端「更多」抽屉共用。 */
function NavGroups({
  groups,
  onNavigate,
}: {
  groups: NavGroup[]
  onNavigate?: () => void
}) {
  return (
    <>
      {groups.map((group) => (
        <div className="nav-group" key={group.label} role="group" aria-label={group.label}>
          <h6 aria-hidden="true">{group.label}</h6>
          {group.items.map((item) => (
            <NavLink
              className={({ isActive }) => `nav-item ${isActive ? 'nav-item-active' : ''}`}
              key={item.to}
              onClick={onNavigate}
              to={item.to}
            >
              <Icon name={item.icon} />
              <span>{item.label}</span>
            </NavLink>
          ))}
        </div>
      ))}
    </>
  )
}

/** 区域切换：普通界面 ↔ 管理后台。仅管理员可见。 */
function AreaSwitch({ variant, onNavigate }: { variant: Variant; onNavigate?: () => void }) {
  const { account } = useAuth()
  if (!account?.is_admin) return null
  return (
    <NavLink
      className="nav-item nav-item-switch"
      onClick={onNavigate}
      to={variant === 'admin' ? '/dashboard' : '/admin/accounts'}
    >
      <Icon name={variant === 'admin' ? 'back' : 'settings'} />
      <span>{variant === 'admin' ? '返回产品' : '进入管理后台'}</span>
    </NavLink>
  )
}

function AccountFooter({
  onNavigate,
  onSignOut,
}: {
  onNavigate?: () => void
  onSignOut: () => void
}) {
  const { account } = useAuth()
  return (
    <div className="sidebar-account">
      <Link className="sidebar-account-link" onClick={onNavigate} to="/account">
        <span aria-hidden="true" className="avatar">
          {account?.display_name.slice(0, 1) || '用'}
        </span>
        <span className="sidebar-account-copy">
          <strong>{account?.display_name}</strong>
          <span>{account?.is_admin ? '管理员' : `@${account?.username}`}</span>
        </span>
        <span className="visually-hidden">账户设置</span>
      </Link>
      <IconButton icon={<Icon name="logout" />} label="退出登录" onClick={onSignOut} />
    </div>
  )
}

/** 顶栏常驻可用积分与钱包入口（用户界面）。 */
function WalletChip() {
  const { wallet } = useWallet()
  const amount = wallet ? formatPointAmount(wallet.spendable_capacity) : '—'
  return (
    <Link
      aria-label={`可用 ${amount} 积分，打开钱包`}
      className="wallet-chip"
      to="/wallet"
    >
      <span>可用</span>
      <strong className="num">{amount}</strong>
      <span>积分</span>
      <span className="wallet-chip-link">钱包</span>
    </Link>
  )
}

export function AppFrame({
  navigation,
  variant,
}: {
  navigation: NavGroup[]
  variant: Variant
}) {
  const { sessionError } = useAuth()
  const { signOut, error: signOutError } = useSignOut()
  const location = useLocation()
  const [moreOpen, setMoreOpen] = useState(false)
  const alertReference = useRef<HTMLDivElement>(null)
  const mainReference = useRef<HTMLElement>(null)
  const admin = variant === 'admin'
  const items = flattenNavigation(navigation)
  const tabItems = items.filter((item) => item.tab)
  const current = findNavItem(navigation, location.pathname)
  const moreActive = Boolean(current && !current.tab)

  useEffect(() => {
    if (signOutError) {
      setMoreOpen(false)
      alertReference.current?.focus()
    }
  }, [signOutError])

  // 路由切换后把焦点移到主内容，键盘与读屏用户不必重新穿过导航
  const previousPath = useRef(location.pathname)
  useEffect(() => {
    if (previousPath.current !== location.pathname) {
      previousPath.current = location.pathname
      mainReference.current?.focus({ preventScroll: true })
    }
  }, [location.pathname])

  // 抽屉打开时跨过 760px 进入桌面布局，主动关闭
  useEffect(() => {
    const desktop = window.matchMedia('(min-width: 761px)')
    const close = (event: MediaQueryListEvent) => {
      if (event.matches) setMoreOpen(false)
    }
    desktop.addEventListener('change', close)
    return () => desktop.removeEventListener('change', close)
  }, [])

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">跳到主要内容</a>
      <aside
        aria-label={admin ? '管理侧边栏' : '产品侧边栏'}
        className="sidebar"
      >
        <Brand subtitle={admin ? '管理后台' : undefined} />
        <nav aria-label={admin ? '管理导航' : '产品导航'} className="sidebar-nav">
          <NavGroups groups={navigation} />
          <AreaSwitch variant={variant} />
        </nav>
        <AccountFooter onSignOut={() => void signOut()} />
      </aside>
      <div className="workspace">
        <header className="topbar">
          <span className="topbar-brand"><Brand /></span>
          <span className="topbar-crumb">{admin ? `管理后台 · ${current?.label ?? ''}` : (current?.label ?? '')}</span>
          {admin ? (
            <Link className="topbar-switch" to="/dashboard">返回产品</Link>
          ) : (
            <WalletChip />
          )}
        </header>
        {(sessionError || signOutError) && (
          <div className="session-alert" ref={alertReference} role="alert" tabIndex={-1}>
            {signOutError || sessionError}
          </div>
        )}
        <main className="page-content" id="main-content" ref={mainReference} tabIndex={-1}>
          <Outlet />
        </main>
      </div>
      <nav aria-label="高频导航" className="bottom-bar">
        {tabItems.map((item) => (
          <NavLink
            className={({ isActive }) => `bottom-tab ${isActive ? 'bottom-tab-active' : ''}`}
            key={item.to}
            to={item.to}
          >
            <Icon name={item.icon} size={20} />
            <span>{item.tab}</span>
          </NavLink>
        ))}
        <button
          aria-expanded={moreOpen}
          aria-haspopup="dialog"
          className={`bottom-tab ${moreActive ? 'bottom-tab-active' : ''}`}
          onClick={() => setMoreOpen(true)}
          type="button"
        >
          <Icon name="more" size={20} />
          <span>更多</span>
        </button>
      </nav>
      <Drawer
        onClose={() => setMoreOpen(false)}
        open={moreOpen}
        placement="bottom"
        title={admin ? '管理导航' : '全部导航'}
      >
        <nav aria-label="全部导航" className="sidebar-nav drawer-nav">
          <NavGroups groups={navigation} onNavigate={() => setMoreOpen(false)} />
          <AreaSwitch onNavigate={() => setMoreOpen(false)} variant={variant} />
        </nav>
        <AccountFooter
          onNavigate={() => setMoreOpen(false)}
          onSignOut={() => {
            setMoreOpen(false)
            void signOut()
          }}
        />
      </Drawer>
    </div>
  )
}
