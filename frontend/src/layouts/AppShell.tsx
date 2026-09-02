import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import { useEphemeralCredential } from '../accounts/EphemeralCredentialProvider'
import { ApiError } from '../api/client'
import { useAuth } from '../auth/AuthProvider'
import { Button } from '../ui/FormControls'
import { Icon, type IconName } from '../ui/Icon'
import { Brand } from './Brand'

type NavigationItem = {
  label: string
  to?: string
  icon: IconName
}

const productNavigation: NavigationItem[] = [
  { label: '账户设置', to: '/account', icon: 'account' },
  { label: 'API Key', icon: 'key' },
  { label: '渠道市场', icon: 'database' },
  { label: '积分钱包', icon: 'wallet' },
]

const adminNavigation: NavigationItem[] = [
  { label: '运营总览', icon: 'settings' },
  { label: '账户与信用', to: '/admin/accounts', icon: 'users' },
  { label: '模型目录', to: '/admin/models', icon: 'database' },
]

export function AppShell({
  children,
  admin = false,
}: {
  children: ReactNode
  admin?: boolean
}) {
  const { account, logout, sessionError } = useAuth()
  const { clearCredential } = useEphemeralCredential()
  const navigate = useNavigate()
  const [menuOpen, setMenuOpen] = useState(false)
  const [signOutError, setSignOutError] = useState('')
  const menuButtonReference = useRef<HTMLButtonElement>(null)
  const signOutAlertReference = useRef<HTMLDivElement>(null)
  const sidebarReference = useRef<HTMLElement>(null)
  const navigation = admin ? adminNavigation : productNavigation
  const bottomNavigation = navigation.filter(
    (item): item is NavigationItem & { to: string } => Boolean(item.to),
  )

  const closeMenu = useCallback((restoreFocus = true) => {
    setMenuOpen((current) => {
      if (current && restoreFocus) {
        requestAnimationFrame(() => menuButtonReference.current?.focus())
      }
      return false
    })
  }, [])

  useEffect(() => {
    if (!menuOpen) return
    const sidebar = sidebarReference.current
    const focusables = Array.from(
      sidebar?.querySelectorAll<HTMLElement>('a[href], button:not([disabled])') ?? [],
    )
    focusables[0]?.focus()
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        closeMenu()
        return
      }
      if (event.key !== 'Tab' || focusables.length === 0) return
      const first = focusables[0]
      const last = focusables[focusables.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('keydown', handleKeyDown)
      document.body.style.overflow = previousOverflow
    }
  }, [closeMenu, menuOpen])

  useEffect(() => {
    const desktop = window.matchMedia('(min-width: 701px)')
    const closeAtDesktop = (event: MediaQueryListEvent) => {
      if (event.matches) closeMenu(false)
    }
    desktop.addEventListener('change', closeAtDesktop)
    return () => desktop.removeEventListener('change', closeAtDesktop)
  }, [closeMenu])

  useEffect(() => {
    if (signOutError) signOutAlertReference.current?.focus()
  }, [signOutError])

  const signOut = async () => {
    setSignOutError('')
    try {
      await logout()
      clearCredential()
      navigate('/login', { replace: true })
    } catch (caught) {
      closeMenu(false)
      setSignOutError(caught instanceof ApiError ? caught.message : '退出失败，请重试')
    }
  }

  return (
    <div className={`app-shell ${menuOpen ? 'menu-open' : ''}`}>
      <button
        aria-controls="app-navigation"
        aria-expanded={menuOpen}
        aria-label={menuOpen ? '关闭导航' : '打开导航'}
        className="mobile-menu-button"
        onClick={() => (menuOpen ? closeMenu() : setMenuOpen(true))}
        ref={menuButtonReference}
        type="button"
      >
        <Icon name="menu" size={20} />
      </button>
      <aside
        aria-label={admin ? '管理侧边栏' : '产品侧边栏'}
        aria-modal={menuOpen || undefined}
        className="sidebar"
        id="app-navigation"
        ref={sidebarReference}
        role={menuOpen ? 'dialog' : undefined}
      >
        <button
          aria-label="关闭导航"
          className="sidebar-close-button"
          onClick={() => closeMenu()}
          type="button"
        >
          <Icon name="arrow-left" size={20} />
        </button>
        <Brand subtitle={admin ? '管理后台' : '受邀实例'} />
        <nav aria-label={admin ? '管理导航' : '产品导航'} className="sidebar-nav">
          {navigation.map((item) =>
            item.to ? (
              <NavLink
                aria-label={item.label}
                className={({ isActive }) =>
                  `nav-item ${isActive ? 'nav-item-active' : ''}`
                }
                key={item.label}
                onClick={() => closeMenu()}
                to={item.to}
              >
                <Icon name={item.icon} />
                <span>{item.label}</span>
              </NavLink>
            ) : (
              <span aria-disabled="true" aria-label={item.label} className="nav-item nav-item-disabled" key={item.label}>
                <Icon name={item.icon} />
                <span>{item.label}</span>
              </span>
            ),
          )}
          {account?.is_admin && (
            <NavLink
              aria-label={admin ? '返回产品' : '进入管理后台'}
              className="nav-item"
              onClick={() => closeMenu()}
              to={admin ? '/account' : '/admin/accounts'}
            >
              <Icon name="arrow-left" />
              <span>{admin ? '返回产品' : '进入管理后台'}</span>
            </NavLink>
          )}
        </nav>
        <div className="sidebar-account">
          <span aria-hidden="true" className="avatar">
            {account?.display_name.slice(0, 1) || '用'}
          </span>
          <span className="sidebar-account-copy">
            <strong>{account?.display_name}</strong>
            <span>@{account?.username}</span>
          </span>
          <Button
            aria-label="退出登录"
            icon={<Icon name="logout" />}
            onClick={() => void signOut()}
            variant="quiet"
          >
            <span className="visually-hidden">退出登录</span>
          </Button>
        </div>
      </aside>
      <div aria-hidden={menuOpen || undefined} className="workspace" inert={menuOpen || undefined}>
        <header className="workspace-bar">
          <span>受邀实例 · Singapore</span>
          <strong>可用 {account?.available_credit ?? '0'} 积分</strong>
        </header>
        {(sessionError || signOutError) && (
          <div
            className="session-alert"
            ref={signOutAlertReference}
            role="alert"
            tabIndex={-1}
          >
            {signOutError || sessionError}
          </div>
        )}
        <main className="page-content">{children}</main>
      </div>
      <nav
        aria-hidden={menuOpen || undefined}
        aria-label="高频导航"
        className="mobile-bottom-nav"
        inert={menuOpen || undefined}
      >
        {bottomNavigation.map((item) => (
          <NavLink
            className={({ isActive }) =>
              `mobile-bottom-item ${isActive ? 'mobile-bottom-item-active' : ''}`
            }
            key={item.label}
            to={item.to}
          >
            <Icon name={item.icon} />
            <span>{item.label}</span>
          </NavLink>
        ))}
        {account?.is_admin && (
          <NavLink
            className="mobile-bottom-item"
            to={admin ? '/account' : '/admin/accounts'}
          >
            <Icon name="arrow-left" />
            <span>{admin ? '产品' : '管理'}</span>
          </NavLink>
        )}
      </nav>
      {menuOpen && (
        <button
          aria-label="关闭导航"
          className="sidebar-scrim"
          onClick={() => closeMenu()}
          type="button"
        />
      )}
    </div>
  )
}
