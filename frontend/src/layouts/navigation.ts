import type { IconName } from '../ui'

export type NavItem = {
  label: string
  to: string
  /** 每个导航项必须使用独立图标（navigation.test.ts 会校验） */
  icon: IconName
  /** 移动端底部 Tab 栏短标签；不设置则只出现在「更多」抽屉中 */
  tab?: string
}

export type NavGroup = {
  label: string
  items: NavItem[]
}

/**
 * 用户导航：使用 API / 共享渠道 / 积分。
 * 「渠道收入」不单设入口：收入由「我的渠道」列表（累计）与渠道详情（按报价）呈现，现有接口没有跨渠道的收入明细。
 */
export const productNavigation: NavGroup[] = [
  {
    label: '使用 API',
    items: [
      { label: '工作台', to: '/dashboard', icon: 'home', tab: '工作台' },
      { label: 'API Keys', to: '/keys', icon: 'key', tab: 'Keys' },
      { label: '调用记录', to: '/calls', icon: 'list' },
      { label: 'API 市场', to: '/market', icon: 'store', tab: '市场' },
    ],
  },
  {
    label: '共享渠道',
    items: [{ label: '我的渠道', to: '/channels', icon: 'server', tab: '渠道' }],
  },
  {
    label: '积分',
    items: [
      { label: '钱包', to: '/wallet', icon: 'wallet', tab: '钱包' },
      { label: 'C2C 市场', to: '/c2c', icon: 'swap' },
    ],
  },
]

export const adminNavigation: NavGroup[] = [
  {
    label: '运营',
    items: [
      { label: '运营台', to: '/admin/ops', icon: 'gauge', tab: '运营台' },
      { label: '账户与信用', to: '/admin/accounts', icon: 'users', tab: '账户' },
      { label: '模型目录', to: '/admin/models', icon: 'layers', tab: '模型' },
    ],
  },
  {
    label: '治理',
    items: [
      { label: '渠道治理', to: '/admin/channels', icon: 'shield', tab: '渠道' },
      { label: '争议处理', to: '/admin/c2c/disputes', icon: 'scale' },
    ],
  },
]

export function flattenNavigation(groups: NavGroup[]) {
  return groups.flatMap((group) => group.items)
}

/** 按路径前缀匹配当前导航项；多个匹配时取最长的，找不到返回 undefined。 */
export function findNavItem(groups: NavGroup[], pathname: string) {
  let best: NavItem | undefined
  for (const item of flattenNavigation(groups)) {
    const matches = pathname === item.to || pathname.startsWith(`${item.to}/`)
    if (matches && (!best || item.to.length > best.to.length)) best = item
  }
  return best
}
