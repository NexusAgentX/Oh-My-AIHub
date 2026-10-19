import { useEffect, useRef, useState } from 'react'
import { Link, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'
import { usePoints } from '../points/queries'
import { amountSign, formatPoints } from '../money/format'
import { ThemeToggle } from '../theme/ThemeSwitcher'
import { Badge, Drawer, Icon, TopbarSlot } from '../ui'
import { adminNavigation, adminTabCount, findAdminNavItem } from '../admin/navigation'
import { WorkspaceSwitcher } from './WorkspaceSwitcher'
import { Brand } from './Brand'
import {
  findMobileTab,
  findNavItem,
  mobileTabs,
  flattenNavigation,
  productNavigation,
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

function NavGroups({ groups, onNavigate }: { groups: NavGroup[]; onNavigate?: () => void }) {
  const { pathname } = useLocation()
  const current = findNavItem(groups, pathname)
  return (
    <>
      {groups.map((group) => (
        <div aria-label={group.label || '快捷入口'} className="nav-group" key={group.label} role="group">
          {group.label && <h6 aria-hidden="true">{group.label}</h6>}
          {group.items.map((item) => (
            <Link
              className={`nav-item ${current?.to === item.to ? 'nav-item-active' : ''}`}
              aria-current={current?.to === item.to ? 'page' : undefined}
              key={item.to}
              to={item.to}
              onClick={onNavigate}
            >
              <Icon name={item.icon} />
              <span>{item.label}</span>
            </Link>
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

/** 侧栏底部账户按钮与菜单：账户设置、管理后台（管理员）、退出。主题切换在顶栏右上角。 */
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
            {location.pathname.startsWith('/admin') ? '管理员' : <>余额 <BalanceText /></>}
          </span>
        </span>
        <Icon name="more" />
      </button>
    </div>
  )
}

function BottomTabs({ tabs, pathname }: { tabs: MobileTab[]; pathname: string }) {
  const current = findMobileTab(tabs, pathname)
  const location = useLocation()
  const recent = useRef(new Map<string, string>())
  useEffect(() => {
    const list = flattenNavigation(productNavigation).some(item => item.to === pathname) || pathname === '/calls/insights'
    const query = new URLSearchParams(location.search)
    if (current && list && !['call', 'tx', 'sell', 'draft_id', 'target_id'].some(key => query.has(key))) recent.current.set(current.to, `${pathname}${location.search}`)
  }, [current, pathname, location.search])
  return (
    <nav aria-label="底部导航" className="bottom-bar">
      {tabs.map((tab) => {
        const active = current?.to === tab.to
        return (
          <Link
            aria-current={active ? 'page' : undefined}
            className={`bottom-tab ${active ? 'bottom-tab-active' : ''}`}
            key={tab.to}
            to={active ? tab.to : recent.current.get(tab.to) ?? tab.to}
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
  const { account, sessionError } = useAuth()
  const { signOut, error: signOutError } = useSignOut()
  const location = useLocation()
  const alertReference = useRef<HTMLDivElement>(null)
  const mainReference = useRef<HTMLElement>(null)
  const [topbarSlot, setTopbarSlot] = useState<HTMLDivElement | null>(null)
  const admin = location.pathname.startsWith('/admin')
  const [moreOpen, setMoreOpen] = useState(false)
  const adminTabs = adminNavigation.slice(0, adminTabCount)
  const adminCurrent = findAdminNavItem(location.pathname)
  useEffect(() => setMoreOpen(false), [location.pathname])

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
    <TopbarSlot.Provider value={topbarSlot}>
    <div className={`app-shell${admin ? ' admin-shell' : ''}`}>
      <a className="skip-link" href="#main-content">
        跳到主要内容
      </a>
      <aside aria-label={admin ? '治理侧边栏' : '产品侧边栏'} className="sidebar">
        <Link aria-label={admin ? '治理首页' : '首页'} className="sidebar-brand" to={admin ? '/admin' : '/home'}>
          <Brand />
        </Link>
        <WorkspaceSwitcher />
        <nav aria-label={admin ? '治理导航' : '产品导航'} className="sidebar-nav">
          <NavGroups groups={navigation} />
        </nav>
        <AccountMenu onSignOut={() => void signOut()} />
      </aside>
      <div className="workspace">
        <header className="topbar">
          <Link aria-label={admin ? '治理首页' : '首页'} className="topbar-brand" to={admin ? '/admin' : '/home'}>
            <Brand />
          </Link>
          {/* 页面标题区把“‹ 上一级”和滚出屏幕后的标题渲染到这里；页面顶部时不重复标题 */}
          <div className="topbar-context" ref={setTopbarSlot} />
          {admin && <Badge tone="warning">治理</Badge>}
          <ThemeToggle />
          <Link className="topbar-account" to="/account" aria-label="账户">
            <span className="avatar" aria-hidden="true">{account?.display_name.slice(0, 1) || '用'}</span>
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
      {admin ? <>
        <nav aria-label="治理高频导航" className="bottom-bar">
          {adminTabs.map(item => <Link key={item.to} to={item.to} className={`bottom-tab${adminCurrent?.to === item.to ? ' bottom-tab-active' : ''}`} aria-current={adminCurrent?.to === item.to ? 'page' : undefined}>
            <Icon name={item.icon} size={20} /><span>{item.label}</span>
          </Link>)}
          <button type="button" className={`bottom-tab${adminCurrent && !adminTabs.includes(adminCurrent) ? ' bottom-tab-active' : ''}`} aria-haspopup="dialog" aria-expanded={moreOpen} onClick={() => setMoreOpen(true)}><Icon name="more" size={20} /><span>更多</span></button>
        </nav>
        <Drawer open={moreOpen} onClose={() => setMoreOpen(false)} placement="bottom" title="治理">
          <nav aria-label="治理全部导航" className="sidebar-nav"><NavGroups groups={navigation} onNavigate={() => setMoreOpen(false)} /></nav>
          <Link className="button button-secondary" to="/home">回到成员</Link>
        </Drawer>
      </> : <BottomTabs pathname={location.pathname} tabs={mobileTabs} />}
    </div>
    </TopbarSlot.Provider>
  )
}
