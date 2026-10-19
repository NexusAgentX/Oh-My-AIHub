import type { IconName } from '../ui'

export type NavItem = { label: string; to: string; icon: IconName; end?: boolean }
export type NavGroup = {
  label: string
  items: NavItem[]
  mobile?: { label: string; to: string; icon: IconName }
  related?: string[]
}

/** 一份分组配置驱动侧栏与移动端域；相关对象路径归入所属域。 */
export const productNavigation: NavGroup[] = [
  { label: '', items: [{ label: '首页', to: '/home', icon: 'home' }], mobile: { label: '首页', to: '/home', icon: 'home' } },
  { label: '使用', items: [{ label: 'API Key', to: '/keys', icon: 'key' }, { label: '调用', to: '/calls', icon: 'list' }], mobile: { label: '使用', to: '/calls', icon: 'list' }, related: ['/usage'] },
  { label: '市场', items: [{ label: '模型', to: '/models', icon: 'store' }, { label: '渠道大厅', to: '/channel-hall', icon: 'layers' }, { label: '供应商', to: '/suppliers', icon: 'globe' }], mobile: { label: '市场', to: '/models', icon: 'store' }, related: ['/groups', '/reports'] },
  { label: '积分', items: [{ label: '钱包', to: '/points', icon: 'wallet' }, { label: '买积分', to: '/points/market', icon: 'coins' }, { label: '我的交易', to: '/points/trades', icon: 'swap' }, { label: '我的卖单', to: '/points/orders', icon: 'sell-order' }], mobile: { label: '积分', to: '/points', icon: 'wallet' } },
  { label: '供应', items: [{ label: '我的渠道', to: '/supply/channels', icon: 'server' }, { label: '收入', to: '/supply/revenue', icon: 'chart' }, { label: '我的贡献', to: '/supply/contributions', icon: 'contribute' }, { label: '检测任务', to: '/supply/detection', icon: 'flask' }], mobile: { label: '供应', to: '/supply/channels', icon: 'server' } },
  { label: '社区', items: [{ label: '论坛', to: '/forum', icon: 'message' }, { label: '工单', to: '/tickets', icon: 'ticket' }] },
]

export type MobileTab = { label: string; to: string; icon: IconName; paths: string[] }
export const mobileTabs: MobileTab[] = productNavigation.flatMap(group => group.mobile ? [{ ...group.mobile, paths: [...group.items.map(item => item.to), ...(group.related ?? [])] }] : [])
export function flattenNavigation(groups: NavGroup[]) { return groups.flatMap(group => group.items) }
function matchesPrefix(pathname: string, prefix: string) { return pathname === prefix || pathname.startsWith(`${prefix}/`) }
export function findNavItem(groups: NavGroup[], pathname: string) {
  return flattenNavigation(groups).filter(item => item.end ? pathname === item.to : matchesPrefix(pathname, item.to)).sort((a, b) => b.to.length - a.to.length)[0]
}
export function findMobileTab(tabs: MobileTab[], pathname: string) { return tabs.find(tab => tab.paths.some(prefix => matchesPrefix(pathname, prefix))) }
