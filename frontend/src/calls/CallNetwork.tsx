import { lazy, Suspense, useState } from 'react'
import { Button } from '../ui'
import type { CallDetail } from '../api/types'
const Replay = lazy(() => import('./NetworkReplay'))

export function CallNetwork({ call, onRefresh }: { call: CallDetail; onRefresh?: () => unknown }) {
  const [open, setOpen] = useState(false)
  return <details className="call-detail-section" onToggle={e => setOpen(e.currentTarget.open)}>
    <summary>请求链路</summary>
    {open && onRefresh && <Button type="button" variant="quiet" size="sm" onClick={() => { void onRefresh() }}>刷新位置</Button>}
    {open && <Suspense fallback={<p role="status">正在加载地图…</p>}><Replay key={call.id} call={call} /></Suspense>}
  </details>
}
