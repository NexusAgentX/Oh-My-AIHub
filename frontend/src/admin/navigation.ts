import type { IconName } from '../ui'

export type AdminNavItem = {
  label: string
  to: string
  icon: IconName
  /** 只匹配完整路径（概览 /admin 不应在子页面高亮） */
  end?: boolean
}

const pages = {
  overview: { label: '待处理', to: '/admin', icon: 'gauge', end: true },
  users: { label: '用户', to: '/admin/users', icon: 'users' },
  points: { label: '积分全局', to: '/admin/points', icon: 'wallet' },
  calls: { label: '调用', to: '/admin/calls', icon: 'list' },
  transactions: { label: '交易浏览', to: '/admin/points/transactions', icon: 'swap' },
  trades: { label: 'C2C 交易', to: '/admin/trades', icon: 'scale' },
  audit: { label: '操作记录', to: '/admin/audit', icon: 'history' },
  contributions: { label: '贡献审核', to: '/admin/contributions', icon: 'contribute' },
  channels: { label: '渠道治理', to: '/admin/channels', icon: 'shield' },
  catalog: { label: '目录治理', to: '/admin/catalog', icon: 'layers' },
  settings: { label: '平台设置', to: '/admin/settings', icon: 'settings' },
  boards: { label: '板块管理', to: '/admin/forum/boards', icon: 'message' },
} satisfies Record<string, AdminNavItem>

/** 前四项进入手机底栏，其余按治理领域进入更多菜单。 */
export const adminNavigation: AdminNavItem[] = Object.values(pages)

export const adminTabCount = 4

/** 当前路径对应的导航项（最长前缀匹配）。 */
export function findAdminNavItem(pathname: string) {
  let best: AdminNavItem | undefined
  for (const item of adminNavigation) {
    const matches = item.end
      ? pathname === item.to || pathname === `${item.to}/`
      : pathname === item.to || pathname.startsWith(`${item.to}/`)
    if (matches && (!best || item.to.length > best.to.length)) best = item
  }
  return best
}

export const adminGroups = [
  { label: '', items: [pages.overview, pages.users] },
  { label: '资金', items: [pages.points, pages.transactions, pages.trades, pages.audit] },
  { label: '目录', items: [pages.contributions, pages.channels, pages.catalog] },
  { label: '观测', items: [pages.calls] },
  { label: '设置', items: [pages.settings, pages.boards] },
]
