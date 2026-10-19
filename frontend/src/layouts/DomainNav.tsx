import { useLocation } from 'react-router-dom'
import { SectionNav } from '../ui/SectionNav'
import { productNavigation } from './navigation'

/** 域内列表切换只放在入口页，编辑和对象详情保留自己的上下文。 */
export function DomainNav({ domain }: { domain: '市场' | '供应' | '社区' }) {
  const { pathname } = useLocation()
  const group = productNavigation.find(group => group.label === domain)
  if (!group || pathname.startsWith('/admin')) return null
  return <SectionNav label={`${domain}分区`} items={group.items.map(item => ({ to: item.to, label: item.label }))} />
}
