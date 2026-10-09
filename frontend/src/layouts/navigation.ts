import type { IconName } from '../ui'

export type NavItem = {
  label: string
  to: string
  /** 每个导航项必须使用独立图标（navigation.test.ts 会校验） */
  icon: IconName
}

export type NavGroup = {
  label: string
  items: NavItem[]
}

/** 桌面侧栏分组。 */
export const productNavigation: NavGroup[] = [
  {
    label: '使用 API',
    items: [
      { label: '首页', to: '/home', icon: 'home' },
      { label: '模型', to: '/models', icon: 'store' },
      { label: 'API Key', to: '/keys', icon: 'key' },
      { label: '用量', to: '/usage', icon: 'chart' },
    ],
  },
  {
    label: '共享',
    items: [{ label: '我的渠道', to: '/channels', icon: 'server' }],
  },
  {
    label: '积分',
    items: [{ label: '积分', to: '/points', icon: 'wallet' }],
  },
  {
    label: '交流',
    items: [
      { label: '论坛', to: '/forum', icon: 'message' },
      { label: '工单', to: '/forum/tickets', icon: 'ticket' },
    ],
  },
]

export type MobileTab = {
  label: string
  to: string
  icon: IconName
  /** 归属到该 Tab 的其他路径前缀（例如「我的」包含 API Key、用量、账户设置） */
  also?: string[]
}

/** 移动端底部 5 个 Tab。 */
export const mobileTabs: MobileTab[] = [
  { label: '首页', to: '/home', icon: 'home' },
  { label: '模型', to: '/models', icon: 'store' },
  { label: '渠道', to: '/channels', icon: 'server' },
  { label: '积分', to: '/points', icon: 'wallet' },
  { label: '我的', to: '/me', icon: 'account', also: ['/keys', '/usage', '/account', '/forum'] },
]

export function flattenNavigation(groups: NavGroup[]) {
  return groups.flatMap((group) => group.items)
}

function matchesPrefix(pathname: string, prefix: string) {
  return pathname === prefix || pathname.startsWith(`${prefix}/`)
}

/** 按路径前缀匹配当前导航项；多个匹配时取最长的，找不到返回 undefined。 */
export function findNavItem(groups: NavGroup[], pathname: string) {
  let best: NavItem | undefined
  for (const item of flattenNavigation(groups)) {
    if (matchesPrefix(pathname, item.to) && (!best || item.to.length > best.to.length)) best = item
  }
  return best
}

/** 当前路径对应的移动端 Tab。 */
export function findMobileTab(tabs: MobileTab[], pathname: string) {
  return tabs.find((tab) => [tab.to, ...(tab.also ?? [])].some((prefix) => matchesPrefix(pathname, prefix)))
}

/** 页面标题（移动端顶栏不显示，桌面顶栏面包屑用）。 */
export function pageTitle(pathname: string) {
  if (matchesPrefix(pathname, '/me')) return '我的'
  if (matchesPrefix(pathname, '/account')) return '账户设置'
  return findNavItem(productNavigation, pathname)?.label ?? ''
}
