import { useLocation } from 'react-router-dom'
import { SectionNav } from '../ui/SectionNav'

export function CallSectionNav({ admin = false }: { admin?: boolean }) {
  const location = useLocation()
  const search = new URLSearchParams(location.search)
  for (const key of ['call', 'category', 'metric', 'group_by', 'split_by', 'bucket_seconds']) search.delete(key)
  const suffix = search.size ? `?${search}` : ''
  const base = admin ? '/admin/calls' : '/calls'
  return <SectionNav label="调用分区" items={[
    { to: `${base}${suffix}`, label: '调用' }, { to: `${base}/insights${suffix}`, label: '分析' },
    ...(!admin ? [{ to: '/keys', label: 'API Key' }] : []),
  ]} />
}

