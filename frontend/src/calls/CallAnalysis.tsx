import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { request, withQuery } from '../api/client'
import type { components } from '../api/schema.gen'
import { formatDateTime } from '../money/time'
import { Button, EmptyState, QueryBoundary, Segmented } from '../ui'
import { diagnosticLabel } from './CallDiagnostics'
import { formatLabels, outcomeLabels } from './labels'
import type { CallListParams } from './queries'

type Values = components['schemas']['AnalysisValues']
type Report = components['schemas']['AnalysisReport']
type Metric = { key: string; label: string; unit?: string; read: (v: Values, seconds: number) => number | null }
const ratio = (a: number, b: number) => (b > 0 ? (a / b) * 100 : null)
const total = (v: Values) => v.input_tokens + v.output_tokens + v.cache_read_tokens + v.cache_write_tokens
const number = (key: keyof Values, label: string, unit?: string): Metric => ({
  key,
  label,
  unit,
  read: (v) => (v[key] === null ? null : Number(v[key])),
})
export const metricGroups: Record<string, Metric[]> = {
  reliability: [
    number('calls', '请求数'),
    { key: 'rpm', label: '每分钟请求', read: (v, s) => (s > 0 ? (v.calls * 60) / s : null) },
    { key: 'success', label: '最终成功率', unit: '%', read: (v) => ratio(v.succeeded, v.finished) },
    { key: 'first', label: '首次尝试成功率', unit: '%', read: (v) => ratio(v.first_succeeded, v.attempted) },
    { key: 'attempt', label: '尝试成功率', unit: '%', read: (v) => ratio(v.attempt_succeeded, v.attempts) },
    { key: 'retry_rate', label: '回退发生率', unit: '%', read: (v) => ratio(v.retried, v.attempted) },
    { key: 'recover', label: '回退挽回率', unit: '%', read: (v) => ratio(v.recovered, v.retried) },
    number('attempts', '上游尝试'),
    number('retry_p50', '回退次数 p50'),
    number('retry_p95', '回退次数 p95'),
    { key: 'delivery', label: '完整交付率', unit: '%', read: (v) => ratio(v.delivered, v.delivery_known) },
    number('billed', '已记账调用'),
    number('unbilled', '成功但未读到用量'),
    number('interrupted', '中断'),
    number('disconnected', '客户端断开'),
  ],
  latency: [
    number('fallback_p50', '回退额外耗时 p50', 'ms'),
    number('fallback_p95', '回退额外耗时 p95', 'ms'),
    ...(['ttft', 'duration', 'overhead'] as const).flatMap((k) =>
      (k === 'overhead' ? [50, 95] : [50, 95, 99]).map((p) =>
        number(
          `${k}_p${p}` as keyof Values,
          `${{ ttft: '流式首响应', duration: '响应总耗时', overhead: '网关额外耗时' }[k]} p${p}`,
          'ms',
        ),
      ),
    ),
    ...['authentication', 'request', 'open_call', 'admission', 'routing', 'pricing', 'settlement', 'client_write'].map(
      (k) => number(`${k}_p95` as keyof Values, `${diagnosticLabel(k)} p95`, 'ms'),
    ),
  ],
  streaming: [
    number('streamed', '流式请求'),
    ...[10, 50, 95].map((p) => number(`speed_p${p}` as keyof Values, `有效输出速率 p${p}`, 'tokens/s')),
    ...[50, 95, 99].map((p) => number(`gap_p${p}` as keyof Values, `单次帧间隔 p95 的 p${p}`, 'ms')),
    number('max_gap_p95', '最大停顿 p95', 'ms'),
    {
      key: 'interrupt_rate',
      label: '中断与断连率',
      unit: '%',
      read: (v) => ratio(v.stream_interrupted, v.stream_finished),
    },
  ],
  tokens: [
    { key: 'tpm', label: '每分钟 tokens', read: (v, s) => (s > 0 ? (total(v) * 60) / s : null) },
    number('input_tokens', '非缓存输入'),
    number('output_tokens', '输出 tokens'),
    number('cache_read_tokens', '缓存读'),
    number('cache_write_tokens', '缓存写'),
    {
      key: 'cache_ratio',
      label: '输入缓存读占比',
      unit: '%',
      read: (v) => ratio(v.cache_read_tokens, v.input_tokens + v.cache_read_tokens + v.cache_write_tokens),
    },
    number('usage_reported', '读到用量'),
    number('usage_missing', '未读到用量'),
    {
      key: 'missing_ratio',
      label: '用量缺失率',
      unit: '%',
      read: (v) => ratio(v.usage_missing, v.usage_reported + v.usage_missing),
    },
  ],
  cost: [
    number('charged', '实际扣费', '积分'),
    number('fee', '手续费', '积分'),
    {
      key: 'average_cost',
      label: '每次请求平均扣费',
      unit: '积分',
      read: (v) => (v.finished > 0 ? Number(v.charged) / v.finished : null),
    },
    ...[50, 95, 99].map((p) => number(`cost_p${p}` as keyof Values, `单次扣费 p${p}`, '积分')),
    {
      key: 'unit_cost',
      label: '每百万 tokens 实际扣费',
      unit: '积分',
      read: (v) => (total(v) > 0 ? (Number(v.charged) / total(v)) * 1e6 : null),
    },
  ],
}
const groupOptions = [
  { key: 'reliability', label: '可靠性' },
  { key: 'latency', label: '耗时' },
  { key: 'streaming', label: '流式' },
  { key: 'tokens', label: '用量' },
  { key: 'cost', label: '费用' },
]
const dimensions: Record<string, string> = {
  model: '模型',
  channel: '最终渠道',
  format: '协议',
  stream: '响应方式',
  key: 'Key',
  tag: '标签',
  account: '账户',
  outcome: '调用结果',
  error: '最后尝试原因',
  error_code: '最后上游错误码',
  status: '最后上游状态',
  stop: '模型结束原因',
  service_tier: '服务档位',
}
function display(value: number | null, unit?: string) {
  return value === null
    ? '—'
    : `${value.toLocaleString('zh-CN', { maximumFractionDigits: unit === '积分' ? 6 : 2 })}${unit ? ` ${unit}` : ''}`
}
export function dimensionLabel(d: string, value: string, label: string) {
  if (!value) return '未提供'
  if (d === 'stream') return value === 'true' ? '流式' : '非流式'
  if (d === 'format') return formatLabels[value as keyof typeof formatLabels] ?? value
  if (d === 'outcome') return outcomeLabels[value as keyof typeof outcomeLabels] ?? value
  return diagnosticLabel(label || value)
}
export function analysisDrill(group: string, value: string, split: string, splitValue: string): CallListParams {
  return {
    dimension: group,
    dimension_value: value,
    second_dimension: split === 'all' ? undefined : split,
    second_value: split === 'all' ? undefined : splitValue,
  }
}
export function CallAnalysis({
  path,
  params,
  onDrill,
  live,
}: {
  path: string
  params: CallListParams
  onDrill: (p: CallListParams, label: string) => void
  live: boolean
}) {
  const [category, setCategory] = useState('reliability')
  const [selected, setSelected] = useState('calls')
  const [group, setGroup] = useState('model')
  const [split, setSplit] = useState('all')
  const [bucket, setBucket] = useState('0')
  const [page, setPage] = useState(0)
  const [trendIndex, setTrendIndex] = useState(0)
  const metrics = metricGroups[category]
  const metric = metrics.find((m) => m.key === selected) ?? metrics[0]
  const eligible = Boolean(params.from && params.to && !params.request_id)
  const query = useQuery({
    queryKey: ['calls', 'analysis', path, params, group, split, bucket],
    queryFn: () =>
      request<Report>(
        withQuery(`${path}/analysis`, { ...params, group_by: group, split_by: split, bucket_seconds: Number(bucket) }),
      ),
    enabled: eligible,
    refetchInterval: live ? 15_000 : false,
    retry: false,
  })
  const dims = Object.entries(dimensions).filter(([key]) => key !== 'account' || path.startsWith('/api/admin/'))
  if (!eligible) return <p className="muted-copy">选择起止时间可查看趋势与分组对比（最长 366 天）。</p>
  return (
    <section className="call-analysis" aria-label="调用分析">
      <div className="call-analysis-controls">
        <Segmented
          label="分析方向"
          options={groupOptions}
          value={category}
          onChange={(v) => {
            setCategory(v)
            setSelected(metricGroups[v][0].key)
          }}
        />
        <label>
          分组
          <select
            className="input"
            aria-label="分析分组"
            value={group}
            onChange={(e) => {
              setGroup(e.target.value)
              setPage(0)
            }}
          >
            {dims.map(([k, l]) => (
              <option key={k} value={k}>
                {l}
              </option>
            ))}
          </select>
        </label>
        <label>
          再按
          <select
            className="input"
            aria-label="交叉维度"
            value={split}
            onChange={(e) => {
              setSplit(e.target.value)
              setPage(0)
            }}
          >
            <option value="all">不拆分</option>
            {dims
              .filter(([k]) => k !== group)
              .map(([k, l]) => (
                <option key={k} value={k}>
                  {l}
                </option>
              ))}
          </select>
        </label>
        <label>
          时间粒度
          <select className="input" value={bucket} onChange={(e) => setBucket(e.target.value)}>
            {[
              ['0', '自动'],
              ['60', '1 分钟'],
              ['300', '5 分钟'],
              ['900', '15 分钟'],
              ['3600', '1 小时'],
              ['21600', '6 小时'],
              ['86400', '1 天'],
            ].map(([k, l]) => (
              <option key={k} value={k}>
                {l}
              </option>
            ))}
          </select>
        </label>
      </div>
      <QueryBoundary errorFallback="分析加载失败，请检查时间范围或缩小分组与时间粒度" query={query}>
        {(report) => {
          const seconds = (Date.parse(report.to) - Date.parse(report.from)) / 1000
          const read = (m: Metric, v: Values) => m.read(v, seconds)
          const trendRead = (row: Report['trend'][number]) =>
            metric.read(
              row.values,
              (Math.min(Date.parse(row.key) + report.bucket_seconds * 1000, Date.parse(report.to)) -
                Math.max(Date.parse(row.key), Date.parse(report.from))) /
                1000,
            )
          const groups = [...report.groups].sort(
            (a, b) => (read(metric, b.values) ?? -1) - (read(metric, a.values) ?? -1),
          )
          const activePage = Math.min(page, Math.max(0, Math.ceil(groups.length / 10) - 1))
          const maximum = Math.max(1, ...report.trend.map((r) => trendRead(r) ?? 0))
          const activeTrend = Math.min(trendIndex, Math.max(0, report.trend.length - 1))
          const point = report.trend[activeTrend]
          return (
            <>
              <div className="analysis-metrics">
                {metrics.map((m) => {
                  const value = read(m, report.current),
                    previous = read(m, report.previous)
                  return (
                    <button
                      className={`analysis-metric ${metric.key === m.key ? 'is-selected' : ''}`}
                      key={m.key}
                      onClick={() => setSelected(m.key)}
                      aria-pressed={metric.key === m.key}
                    >
                      <span>{m.label}</span>
                      <strong className="num">{display(value, m.unit)}</strong>
                      <small>前期 {display(previous, m.unit)}</small>
                    </button>
                  )
                })}
              </div>
              <p className="muted-copy">
                前期：{formatDateTime(report.previous_from)} — {formatDateTime(report.previous_to)}
                。分位来自当前筛选的原始样本；— 表示无样本或未采集。尝试指标包含这些请求经过的全部渠道。
              </p>
              {report.current.calls === 0 ? (
                <EmptyState mascot={false} title="这段时间没有调用" />
              ) : (
                <>
                  <h3>{metric.label}趋势</h3>
                  <div className="analysis-trend" role="group" aria-label={`${metric.label}趋势`}>
                    {report.trend.map((row, index) => {
                      const value = trendRead(row)
                      return (
                        <button
                          key={row.key}
                          tabIndex={index === activeTrend ? 0 : -1}
                          aria-pressed={index === activeTrend}
                          aria-label={`${formatDateTime(row.key)} · ${metric.label} ${display(value, metric.unit)}`}
                          onFocus={() => setTrendIndex(index)}
                          onClick={() => setTrendIndex(index)}
                          onKeyDown={(event) => {
                            const next = event.key === 'ArrowRight' ? Math.min(index + 1, report.trend.length - 1) : event.key === 'ArrowLeft' ? Math.max(0, index - 1) : event.key === 'Home' ? 0 : event.key === 'End' ? report.trend.length - 1 : -1
                            if (next < 0) return
                            event.preventDefault()
                            setTrendIndex(next)
                            event.currentTarget.parentElement?.querySelectorAll('button')[next]?.focus()
                          }}
                        >
                          <span
                            className="analysis-trend-bar"
                            style={{
                              height: `${value === null ? 0 : value === 0 ? 0 : Math.max(2, (value / maximum) * 100)}%`,
                            }}
                          />
                          <span className="visually-hidden">
                            {formatDateTime(row.key)}：{display(value, metric.unit)}
                          </span>
                        </button>
                      )
                    })}
                  </div>
                  {point && <div className="analysis-reading">
                    <output aria-live="polite">{formatDateTime(point.key)} · {metric.label} {display(trendRead(point), metric.unit)}</output>
                    <Button variant="secondary" onClick={() => {
                            const end = new Date(
                              Math.min(
                                new Date(point.key).getTime() + report.bucket_seconds * 1000,
                                new Date(report.to).getTime(),
                              ),
                            ).toISOString()
                            onDrill({
                              from: new Date(
                                Math.max(new Date(point.key).getTime(), new Date(report.from).getTime()),
                              ).toISOString(),
                              to: end,
                            }, `${formatDateTime(point.key)} — ${formatDateTime(end)}`)
                    }}>查看此时段调用</Button>
                  </div>}
                  <div className="analysis-axis">
                    <span>{formatDateTime(report.from)}</span>
                    <span>{formatDateTime(report.to)}</span>
                  </div>
                </>
              )}
              <h3>
                按{dimensions[group]}
                {split !== 'all' ? ` × ${dimensions[split]}` : ''}对比
              </h3>
              <div className="analysis-comparison" data-count-metric={metric.key === 'calls'}>
                <div className="analysis-comparison-head">
                  <span>分组</span>
                  <span>{metric.label}</span>
                  {metric.key !== 'calls' && <span>请求</span>}
                  <span>成功率</span>
                </div>
                {groups.slice(activePage * 10, activePage * 10 + 10).map((row) => (
                  <button
                    key={`${row.key}/${row.split_key}`}
                    className="analysis-comparison-row"
                    onClick={() => onDrill(analysisDrill(group, row.key, split, row.split_key),
                      [dimensionLabel(group, row.key, row.label), ...(split !== 'all' ? [dimensionLabel(split, row.split_key, row.split_label)] : [])].join(' · '))}
                  >
                    <strong>
                      {dimensionLabel(group, row.key, row.label)}
                      {split !== 'all' && <small>{dimensionLabel(split, row.split_key, row.split_label)}</small>}
                    </strong>
                    <span className="num"><small className="comparison-label">{metric.label}</small>{display(read(metric, row.values), metric.unit)}</span>
                    {metric.key !== 'calls' && <span className="num"><small className="comparison-label">请求</small>{row.values.calls} 次</span>}
                    <span className="num"><small className="comparison-label">成功率</small>{display(ratio(row.values.succeeded, row.values.finished), '%')}</span>
                  </button>
                ))}
              </div>
              {groups.length > 10 && (
                <nav className="channel-record-pages" aria-label="分析分组分页">
                  <Button variant="quiet" disabled={activePage === 0} onClick={() => setPage(activePage - 1)}>
                    上一页
                  </Button>
                  <span>
                    {activePage + 1} / {Math.ceil(groups.length / 10)} · {groups.length} 组
                  </span>
                  <Button
                    variant="quiet"
                    disabled={(activePage + 1) * 10 >= groups.length}
                    onClick={() => setPage(activePage + 1)}
                  >
                    下一页
                  </Button>
                </nav>
              )}
            </>
          )
        }}
      </QueryBoundary>
    </section>
  )
}
