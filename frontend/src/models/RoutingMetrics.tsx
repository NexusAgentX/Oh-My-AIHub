import type { components } from '../api/schema.gen'
import { formatLabels } from '../calls'
import { formatMs, formatRatio } from '../money/format'

type Metric = components['schemas']['ModelRoutingMetric']

export function RoutingMetrics({ rows }: { rows?: Metric[] }) {
  if (!rows?.length) return <p className="muted-copy routing-model-metrics-empty">该模型暂无有效统计 · 延迟和速率未知</p>
  return <div className="routing-model-metrics">{rows.map(row => <section key={`${row.format}/${row.tier}/${row.stream}`}>
    <p className="muted-copy">{formatLabels[row.format]} · {row.stream ? '流式' : '非流式'} · {row.tier || '默认档位'}</p>
    <dl className="routing-metrics">
      <div><dt>24h 可用率</dt><dd>{row.success_rate == null ? '未知' : formatRatio(String(row.success_rate))}<small> · {row.attempts} 次{row.attempts < row.min_samples && '，样本不足'}</small></dd></div>
      <div><dt>首有效输出 p50</dt><dd>{row.first_output_p50_ms == null ? '未知' : formatMs(row.first_output_p50_ms)}<small> · {row.first_output_samples} 样本{row.first_output_samples > 0 && row.first_output_samples < row.min_samples && '，暂不用于排序'}</small></dd></div>
      <div><dt>有效输出速率 p50</dt><dd>{row.output_tps_p50 == null ? '未知' : `${row.output_tps_p50.toFixed(1)} tokens/s`}<small> · {row.output_samples} 样本{row.output_samples > 0 && row.output_samples < row.min_samples && '，暂不用于排序'}</small></dd></div>
    </dl>
  </section>)}</div>
}
