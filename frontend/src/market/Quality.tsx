import { QualityStats } from '../sharing/QualityStats'
import { Disclosure, QueryBoundary } from '../ui'
import { useMarketChannelNames } from './queries'

function SupplierQuality({ id }: { id: string }) {
  const names = useMarketChannelNames(id)
  return <QueryBoundary query={names} errorFallback="渠道筛选加载失败">{channels => <QualityStats key={id} path={`/api/suppliers/${encodeURIComponent(id)}`} channels={channels} />}</QueryBoundary>
}

/** Mount on first opening, then retain date/filter state while collapsed. */
export function MarketQualitySection({ kind, id }: { kind: 'supplier' | 'channel'; id: string }) {
  return <Disclosure title="调用质量" lazy>
    {kind === 'supplier' ? <SupplierQuality id={id} /> : <QualityStats key={id} path={`/api/channel-hall/${encodeURIComponent(id)}`} />}
  </Disclosure>
}
