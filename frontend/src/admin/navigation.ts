import type { IconName } from '../ui'

export type AdminNavItem = {
  label: string
  to: string
  icon: IconName
  /** 只匹配完整路径（概览 /admin 不应在子页面高亮） */
  end?: boolean
}

/** 管理后台导航：前 4 项进入移动端底部 Tab，其余进「更多」。 */
export const adminNavigation: AdminNavItem[] = [
  { label: '概览', to: '/admin', icon: 'gauge', end: true },
  { label: '调用', to: '/admin/calls', icon: 'list' },
  { label: '积分', to: '/admin/points', icon: 'wallet' },
  { label: '用户', to: '/admin/users', icon: 'users' },
  { label: '模型', to: '/admin/models', icon: 'layers' },
  { label: '供应商', to: '/admin/suppliers', icon: 'globe' },
  { label: '渠道', to: '/admin/channels', icon: 'shield' },
  {label:'分组渠道',to:'/admin/group-channels',icon:'server'},
  { label: '申诉', to: '/admin/disputes', icon: 'scale' },
  { label: '设置', to: '/admin/settings', icon: 'settings' },
 {label:'典型费用',to:'/admin/typical-costs',icon:'chart'},
 {label:'检测参数',to:'/admin/detection-search',icon:'flask'},
 {label:'贡献审核',to:'/admin/contributions',icon:'contribute'},
]

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
