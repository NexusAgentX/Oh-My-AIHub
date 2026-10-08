import type { CallStats } from '../api/types'
import { formatMs, formatPoints, formatRatio, formatTokens } from '../money/format'

/** 当前筛选条件下的汇总：请求数、成功率、首字 p50/p95、总 tokens、费用。 */
export function CallStatsBar({ stats, chargedLabel = '费用' }: { stats: CallStats; chargedLabel?: string }) {
  const items: Array<[string, string]> = [
    ['请求', stats.calls.toLocaleString('zh-CN')],
    ['成功率', formatRatio(stats.success_rate)],
    ['首字 p50 / p95', `${formatMs(stats.ttft_p50_ms)} / ${formatMs(stats.ttft_p95_ms)}`],
    ['tokens', formatTokens(stats.total_tokens)],
    [chargedLabel, formatPoints(stats.charged)],
  ]
  return (
    <dl className="call-stats" aria-label="筛选汇总">
      {items.map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd className="num">{value}</dd>
        </div>
      ))}
    </dl>
  )
}
