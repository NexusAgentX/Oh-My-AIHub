import { SectionNav } from '../ui/SectionNav'

export function FinanceNav() {
  return <SectionNav label="资金分区" items={[{ to: '/admin/points', label: '积分全局' }, { to: '/admin/points/transactions', label: '交易浏览' }, { to: '/admin/trades', label: 'C2C 交易' }, { to: '/admin/audit', label: '操作记录' }]} />
}
export function SettingsNav() {
  return <SectionNav label="设置分区" items={[{ to: '/admin/settings', label: '平台设置' }, { to: '/admin/settings/typical-costs', label: '典型费用' }, { to: '/admin/settings/detection', label: '检测参数' }]} />
}
