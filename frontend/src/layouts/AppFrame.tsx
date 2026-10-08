import { useEffect, useRef, useState } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'
import { Drawer, Icon, IconButton } from '../ui'
import { usePoints } from '../home/queries'
import { formatPointAmount } from '../money/format'
import { Brand } from './Brand'
import { findNavItem, flattenNavigation, type NavGroup } from './navigation'

function useSignOut() {
  const { logout } = useAuth()
  const navigate = useNavigate()
  const [error, setError] = useState('')
  const signOut = async () => {
    setError('')
    try {
      await logout()
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
      <Link className="sidebar-account-link" onClick={onNavigate} to="/home">
        <span aria-hidden="true" className="avatar">
          {account?.display_name.slice(0, 1) || '用'}
        </span>
        <span className="sidebar-account-copy">
          <strong>{account?.display_name}</strong>
          <span>{account?.is_admin ? '管理员' : `@${account?.username}`}</span>
        </span>
        <span className="visually-hidden">首页</span>
      </Link>
      <IconButton icon={<Icon name="logout" />} label="退出登录" onClick={onSignOut} />
    </div>
  )
}

/** 顶栏常驻可透支额度。 */
function PointsChip() {
  const { data } = usePoints()
  const amount = data ? formatPointAmount(data.available) : '—'
  return (
    <Link aria-label={`可用 ${amount} 积分`} className="wallet-chip" to="/home">
      <span>可用</span>
      <strong className="num">{amount}</strong>
      <span>积分</span>
    </Link>
  )
}

export function AppFrame({ navigation }: { navigation: NavGroup[] }) {
  const { sessionError } = useAuth()
  const { signOut, error: signOutError } = useSignOut()
  const location = useLocation()
  const [moreOpen, setMoreOpen] = useState(false)
  const alertReference = useRef<HTMLDivElement>(null)
  const mainReference = useRef<HTMLElement>(null)
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
      <aside aria-label="产品侧边栏" className="sidebar">
        <Brand />
        <nav aria-label="产品导航" className="sidebar-nav">
          <NavGroups groups={navigation} />
        </nav>
        <AccountFooter onSignOut={() => void signOut()} />
      </aside>
      <div className="workspace">
        <header className="topbar">
          <span className="topbar-brand"><Brand /></span>
          <span className="topbar-crumb">{current?.label ?? ''}</span>
          <PointsChip />
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
        title="全部导航"
      >
        <nav aria-label="全部导航" className="sidebar-nav drawer-nav">
          <NavGroups groups={navigation} onNavigate={() => setMoreOpen(false)} />
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
