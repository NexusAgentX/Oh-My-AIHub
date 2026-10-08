import { useEffect, useRef, useState } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'
import { usePoints } from '../points/queries'
import { amountSign, formatPoints } from '../money/format'
import { Icon } from '../ui'
import { Brand } from './Brand'
import {
  findMobileTab,
  mobileTabs,
  pageTitle,
  type MobileTab,
  type NavGroup,
} from './navigation'

export function useSignOut() {
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

function NavGroups({ groups }: { groups: NavGroup[] }) {
  return (
    <>
      {groups.map((group) => (
        <div aria-label={group.label} className="nav-group" key={group.label} role="group">
          <h6 aria-hidden="true">{group.label}</h6>
          {group.items.map((item) => (
            <NavLink
              className={({ isActive }) => `nav-item ${isActive ? 'nav-item-active' : ''}`}
              key={item.to}
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

/** 余额：负数红色；未加载时显示 —。 */
function BalanceText() {
  const { data } = usePoints()
  if (!data) return <span className="num">—</span>
  return (
    <span className={`num ${amountSign(data.balance) < 0 ? 'amount-negative' : ''}`}>
      {formatPoints(data.balance, { digits: 2 })}
    </span>
  )
}

/** 侧栏底部账户按钮与菜单：账户设置、管理后台（管理员）、退出。 */
function AccountMenu({ onSignOut }: { onSignOut: () => void }) {
  const { account } = useAuth()
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  const location = useLocation()

  useEffect(() => setOpen(false), [location.pathname])
  useEffect(() => {
    if (!open) return
    const onPointer = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false)
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpen(false)
        button.current?.focus()
      }
    }
    document.addEventListener('pointerdown', onPointer)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('pointerdown', onPointer)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div className="sidebar-account" ref={root}>
      {open && (
        <div aria-label="账户菜单" className="account-menu" role="menu">
          <Link className="account-menu-item" role="menuitem" to="/account">
            <Icon name="settings" />
            账户设置
          </Link>
          {account?.is_admin && (
            <Link className="account-menu-item" role="menuitem" to="/admin">
              <Icon name="shield" />
              管理后台
            </Link>
          )}
          <button
            className="account-menu-item"
            onClick={() => {
              setOpen(false)
              onSignOut()
            }}
            role="menuitem"
            type="button"
          >
            <Icon name="logout" />
            退出
          </button>
        </div>
      )}
      <button
        aria-expanded={open}
        aria-haspopup="menu"
        className="sidebar-account-button"
        onClick={() => setOpen((value) => !value)}
        ref={button}
        type="button"
      >
        <span aria-hidden="true" className="avatar">
          {account?.display_name.slice(0, 1) || '用'}
        </span>
        <span className="sidebar-account-copy">
          <strong>{account?.display_name}</strong>
          <span>
            余额 <BalanceText />
          </span>
        </span>
        <Icon name="more" />
      </button>
    </div>
  )
}

function BottomTabs({ tabs, pathname }: { tabs: MobileTab[]; pathname: string }) {
  const current = findMobileTab(tabs, pathname)
  return (
    <nav aria-label="底部导航" className="bottom-bar">
      {tabs.map((tab) => {
        const active = current?.to === tab.to
        return (
          <Link
            aria-current={active ? 'page' : undefined}
            className={`bottom-tab ${active ? 'bottom-tab-active' : ''}`}
            key={tab.to}
            to={tab.to}
          >
            <Icon name={tab.icon} size={20} />
            <span>{tab.label}</span>
          </Link>
        )
      })}
    </nav>
  )
}

export function AppFrame({ navigation }: { navigation: NavGroup[] }) {
  const { sessionError } = useAuth()
  const { signOut, error: signOutError } = useSignOut()
  const location = useLocation()
  const alertReference = useRef<HTMLDivElement>(null)
  const mainReference = useRef<HTMLElement>(null)

  useEffect(() => {
    if (signOutError) alertReference.current?.focus()
  }, [signOutError])

  // 路由切换后把焦点移到主内容，键盘与读屏用户不必重新穿过导航
  const previousPath = useRef(location.pathname)
  useEffect(() => {
    if (previousPath.current !== location.pathname) {
      previousPath.current = location.pathname
      mainReference.current?.focus({ preventScroll: true })
    }
  }, [location.pathname])

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        跳到主要内容
      </a>
      <aside aria-label="产品侧边栏" className="sidebar">
        <Link aria-label="首页" className="sidebar-brand" to="/home">
          <Brand />
        </Link>
        <nav aria-label="产品导航" className="sidebar-nav">
          <NavGroups groups={navigation} />
        </nav>
        <AccountMenu onSignOut={() => void signOut()} />
      </aside>
      <div className="workspace">
        <header className="topbar">
          <Link aria-label="首页" className="topbar-brand" to="/home">
            <Brand />
          </Link>
          <span className="topbar-crumb">{pageTitle(location.pathname)}</span>
          <Link className="wallet-chip" to="/points">
            <span>余额</span>
            <strong>
              <BalanceText />
            </strong>
          </Link>
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
      <BottomTabs pathname={location.pathname} tabs={mobileTabs} />
    </div>
  )
}
