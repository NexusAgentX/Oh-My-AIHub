import { useEffect, useRef, useState } from 'react'
import { Link, NavLink, Outlet, useLocation } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { Brand } from '../layouts/Brand'
import { ThemeToggle } from '../theme/ThemeSwitcher'
import { Drawer, Icon, PageBackSlot } from '../ui'
import { adminNavigation, adminTabCount, findAdminNavItem, type AdminNavItem } from './navigation'

function NavItems({ items, onNavigate }: { items: AdminNavItem[]; onNavigate?: () => void }) {
  return (
    <div className="nav-group" role="group" aria-label="管理后台">
      {items.map((item) => (
        <NavLink
          className={({ isActive }) => `nav-item ${isActive ? 'nav-item-active' : ''}`}
          end={item.end}
          key={item.to}
          onClick={onNavigate}
          to={item.to}
        >
          <Icon name={item.icon} />
          <span>{item.label}</span>
        </NavLink>
      ))}
    </div>
  )
}

function AdminFooter({ showBackLink = false, onNavigate }: { showBackLink?: boolean; onNavigate?: () => void }) {
  const { account } = useAuth()
  return (
    <div className="admin-sidebar-footer">
      <div className="admin-identity">
        <span aria-hidden="true" className="avatar">
          {account?.display_name.slice(0, 1) || '管'}
        </span>
        <span className="sidebar-account-copy">
          <strong>{account?.display_name}</strong>
          <span>管理员</span>
        </span>
      </div>
      {showBackLink && (
        <Link className="admin-back-link" onClick={onNavigate} to="/home">
          <Icon name="back" />
          <span>返回产品</span>
        </Link>
      )}
    </div>
  )
}

/** 管理后台外壳：与用户界面同样的侧栏与底部 Tab 样式（layout.css），导航独立。 */
export function AdminFrame() {
  const { sessionError } = useAuth()
  const location = useLocation()
  const [moreOpen, setMoreOpen] = useState(false)
  const mainReference = useRef<HTMLElement>(null)
  const current = findAdminNavItem(location.pathname)
  const tabs = adminNavigation.slice(0, adminTabCount)
  const moreActive = Boolean(current && !tabs.includes(current))

  const previousPath = useRef(location.pathname)
  useEffect(() => {
    if (previousPath.current !== location.pathname) {
      previousPath.current = location.pathname
      mainReference.current?.focus({ preventScroll: true })
    }
  }, [location.pathname])

  useEffect(() => {
    const desktop = window.matchMedia('(min-width: 768px)')
    const close = (event: MediaQueryListEvent) => {
      if (event.matches) setMoreOpen(false)
    }
    desktop.addEventListener('change', close)
    return () => desktop.removeEventListener('change', close)
  }, [])

  const [backSlot, setBackSlot] = useState<HTMLDivElement | null>(null)

  return (
    <PageBackSlot.Provider value={backSlot}>
    <div className="app-shell admin-shell">
      <a className="skip-link" href="#main-content">跳到主要内容</a>
      <aside aria-label="管理后台侧边栏" className="sidebar">
        <Brand subtitle="管理后台" />
        <nav aria-label="管理后台导航" className="sidebar-nav">
          <NavItems items={adminNavigation} />
        </nav>
        <AdminFooter />
      </aside>
      <div className="workspace">
        <header className="topbar">
          <span className="topbar-brand"><Brand /></span>
          {/* 详情页的“‹ 上一级”渲染到这里；当前栏目已在侧栏高亮、页面标题里写明，不再重复 */}
          <div className="topbar-back" ref={setBackSlot} />
          <ThemeToggle />
          <Link aria-label="返回产品" className="admin-topbar-back" to="/home">
            <Icon name="back" />
            <span>返回产品</span>
          </Link>
        </header>
        {sessionError && (
          <div className="session-alert" role="alert">
            {sessionError}
          </div>
        )}
        <main className="page-content" id="main-content" ref={mainReference} tabIndex={-1}>
          <Outlet />
        </main>
      </div>
      <nav aria-label="管理后台高频导航" className="bottom-bar">
        {tabs.map((item) => (
          <NavLink
            className={({ isActive }) => `bottom-tab ${isActive ? 'bottom-tab-active' : ''}`}
            end={item.end}
            key={item.to}
            to={item.to}
          >
            <Icon name={item.icon} size={20} />
            <span>{item.label}</span>
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
      <Drawer onClose={() => setMoreOpen(false)} open={moreOpen} placement="bottom" title="管理后台">
        <nav aria-label="管理后台全部导航" className="sidebar-nav drawer-nav">
          <NavItems items={adminNavigation} onNavigate={() => setMoreOpen(false)} />
        </nav>
        <AdminFooter showBackLink onNavigate={() => setMoreOpen(false)} />
      </Drawer>
    </div>
    </PageBackSlot.Provider>
  )
}
