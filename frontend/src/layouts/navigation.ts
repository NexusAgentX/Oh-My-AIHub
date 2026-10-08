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
 * 用户导航。产品重写（Epic #170）期间只保留占位首页；
 * 完整的侧栏分组由 Feature D（用户界面）与 Feature E（管理后台）重建。
 */
export const productNavigation: NavGroup[] = [
  {
    label: '概览',
    items: [{ label: '首页', to: '/home', icon: 'home', tab: '首页' }],
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
