import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { request, withQuery } from '../api/client'
import type { components } from '../api/schema.gen'
import { formatLabels } from '../calls/labels'
import { formatDateTime } from '../money/time'
import { BarChart, Button, Card, EmptyState, QueryBoundary, Segmented, SelectField, TextField } from '../ui'

type Values = components['schemas']['DirectoryValues']
type Report = components['schemas']['DirectoryReport']
type QualityMetric = { key: string; label: string; unit?: string; sample?: keyof Values; read: (v: Values) => number | null }
const count = (key: keyof Values, label: string): QualityMetric => ({ key, label, read: (v) => v[key] })
export const qualityMetrics: QualityMetric[] = [
  count('attempts', '上游尝试'),
  { key: 'success_rate', label: '尝试成功率', unit: '%', sample: 'counted', read: (v) => v.counted ? v.succeeded / v.counted * 100 : null },
  count('succeeded', '成功尝试'), count('failed', '失败尝试'), count('excluded', '不计入成功率'), count('streamed', '流式尝试'),
  ...(['ttft', 'duration', 'speed', 'gap'] as const).flatMap((key) => (key === 'speed' ? [10, 50, 95] : [50, 95, 99]).map((p): QualityMetric => ({
    key: `${key}_p${p}`, label: `${{ ttft: '流式首响应', duration: '尝试耗时', speed: '输出速度', gap: '最大帧间隔' }[key]} P${p}`,
    unit: key === 'speed' ? 'tokens/s' : 'ms', sample: `${key}_samples`, read: (v) => v[`${key}_p${p}` as keyof Values],
  }))),
]
export function qualityValue(value: number | null, unit?: string) {
  return value === null ? '—' : `${value.toLocaleString('zh-CN', { maximumFractionDigits: 2 })}${unit ? ` ${unit}` : ''}`
}
const dimensions = { model: '模型', channel: '渠道', format: '协议', stream: '响应方式', result: '尝试结果', status: '上游状态码' }
const results: Record<string, string> = { completed: '成功', upstream_error: '上游错误', connect_error: '连接错误', timeout_ttft: '首响应超时', timeout_total: '总耗时超时', client_error: '请求错误', client_disconnected: '客户端断开', interrupted: '流式中断', unknown: '未知' }
const categories = [{ key: 'reliability', label: '可靠性' }, { key: 'latency', label: '耗时' }, { key: 'streaming', label: '流式' }]
function groupLabel(group: string, key: string, label: string) {
  if (group === 'result') return results[key] ?? '未知'
  if (group === 'status' && key === 'none') return '无响应状态码'
  if (group === 'stream') return key === 'true' ? '流式' : '非流式'
  if (group === 'format') return formatLabels[key as keyof typeof formatLabels] ?? key
  return label || key || '未提供'
}
const initialFilters = { model: '', channel: '', format: '', stream: '', result: '', status: '' }
type Filters = typeof initialFilters
function presetWindow(days: string) {
  const to = new Date()
  return { from: new Date(to.getTime() - Number(days) * 86400000).toISOString(), to: to.toISOString() }
}
function localInput(iso: string) {
  const date = new Date(iso)
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16)
}
export function QualityStats({ path, channels }: { path: string; channels?: { id: string; name: string }[] }) {
  const [range, setRange] = useState('7')
  const [window, setWindow] = useState(() => presetWindow('7'))
  const [dates, setDates] = useState(() => ({ from: localInput(window.from), to: localInput(window.to) }))
  const [draft, setDraft] = useState<Filters>(initialFilters)
  const [filters, setFilters] = useState<Filters>(initialFilters)
  const [group, setGroup] = useState(channels ? 'channel' : 'model')
  const [category, setCategory] = useState('reliability')
  const [selected, setSelected] = useState('attempts')
  const [page, setPage] = useState(0)
  const [error, setError] = useState('')
  const visibleMetrics = qualityMetrics.filter((m, i) => category === 'reliability' ? i < 6 : category === 'latency' ? m.key.startsWith('ttft') || m.key.startsWith('duration') : m.key.startsWith('speed') || m.key.startsWith('gap'))
  const metric = qualityMetrics.find((m) => m.key === selected)!
  const query = useQuery({ queryKey: ['sharing', 'quality', path, window, filters, group], queryFn: () => request<Report>(withQuery(`${path}/analysis`, { ...window, ...filters, group_by: group })), retry: false, staleTime: 30_000 })
  const change = (key: keyof Filters, value: string) => setDraft((d) => ({ ...d, [key]: value }))
  const apply = () => {
    if (range === 'custom') {
      const from = new Date(dates.from), to = new Date(dates.to)
      if (!Number.isFinite(from.getTime()) || !Number.isFinite(to.getTime()) || from >= to || to.getTime() - from.getTime() > 90 * 86400000) { setError('请选择有效的起止时间，最长 90 天'); return }
      setWindow({ from: from.toISOString(), to: to.toISOString() })
    } else setWindow(presetWindow(range))
    setFilters(draft); setPage(0); setError('')
  }
  return <Card title="质量统计" className="quality-stats">
    <form onSubmit={(e) => { e.preventDefault(); apply() }}>
      <div className="quality-filters">
        <SelectField label="时间范围" value={range} onChange={(e) => { const value = e.target.value; setRange(value); if (value !== 'custom') { const next = presetWindow(value); setWindow(next); setDates({ from: localInput(next.from), to: localInput(next.to) }); setPage(0); setError('') } }}>
          <option value="1">近 24 小时</option><option value="7">近 7 天</option><option value="30">近 30 天</option><option value="custom">自定义</option>
        </SelectField>
        <TextField label="模型" value={draft.model} placeholder="全部模型（精确筛选）" onChange={(e) => change('model', e.target.value)} />
        <SelectField label="协议" value={draft.format} onChange={(e) => change('format', e.target.value)}><option value="">全部</option>{Object.entries(formatLabels).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</SelectField>
        <SelectField label="响应方式" value={draft.stream} onChange={(e) => change('stream', e.target.value)}><option value="">全部</option><option value="true">流式</option><option value="false">非流式</option></SelectField>
        {channels && <SelectField label="渠道" value={draft.channel} onChange={(e) => change('channel', e.target.value)}><option value="">全部关联渠道</option>{channels.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}</SelectField>}
        <SelectField label="尝试结果" value={draft.result} onChange={(e) => change('result', e.target.value)}><option value="">全部</option>{Object.entries(results).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</SelectField>
        <TextField label="上游状态码" value={draft.status} placeholder="全部；无响应填 none" onChange={(e) => change('status', e.target.value)} />
        {range === 'custom' && <><TextField label="开始时间" type="datetime-local" value={dates.from} onChange={(e) => setDates((d) => ({ ...d, from: e.target.value }))} /><TextField label="结束时间" type="datetime-local" value={dates.to} onChange={(e) => setDates((d) => ({ ...d, to: e.target.value }))} /></>}
      </div>
      <div className="sharing-line"><Button type="submit" variant="secondary" loading={query.isFetching}>应用筛选</Button><Button type="button" variant="quiet" onClick={() => { setDraft(initialFilters); setFilters(initialFilters); setPage(0); setError('') }}>清空筛选</Button><Button type="button" variant="quiet" onClick={() => { if (range === 'custom') void query.refetch(); else setWindow(presetWindow(range)) }}>刷新</Button></div>
      {error && <p role="alert">{error}</p>}
    </form>
    <QueryBoundary query={query} errorFallback="质量统计加载失败">{(report) => {
      const sorted = [...report.groups].sort((a, b) => (metric.read(b.values) ?? -Infinity) - (metric.read(a.values) ?? -Infinity) || a.key.localeCompare(b.key))
      const last = Math.max(0, Math.ceil(sorted.length / 20) - 1), currentPage = Math.min(page, last)
      return <>
        <p className="muted-copy">{formatDateTime(report.from)} — {formatDateTime(report.to)} · 对比前一等长时段</p>
        <Segmented label="统计方向" value={category} options={categories} onChange={(value) => { setCategory(value); setSelected(value === 'reliability' ? 'attempts' : value === 'latency' ? 'ttft_p50' : 'speed_p50') }} />
        <dl className="quality-values">{visibleMetrics.map((m) => <div key={m.key}><dt>{m.label}</dt><dd className="num">{qualityValue(m.read(report.current), m.unit)}</dd><small>前期 {qualityValue(m.read(report.previous), m.unit)}{m.sample && ` · 样本 ${report.current[m.sample] ?? 0}`}</small></div>)}</dl>
        {report.current.attempts === 0 ? <EmptyState mascot={false} title="所选范围暂无尝试样本" /> : <>
          <div className="quality-controls">
            <SelectField label="趋势与对比指标" value={selected} onChange={(e) => { setSelected(e.target.value); setPage(0) }}>{qualityMetrics.map((m) => <option key={m.key} value={m.key}>{m.label}</option>)}</SelectField>
            <SelectField label="统计维度" value={group} onChange={(e) => { setGroup(e.target.value); setPage(0) }}>{Object.entries(dimensions).filter(([key]) => key !== 'channel' || channels).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</SelectField>
          </div>
          <h3>{metric.label}趋势</h3>
          <div className="quality-trend"><BarChart label={`${metric.label}趋势`} data={report.trend.map((row) => ({ key: row.key, label: new Date(row.key).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', ...(report.bucket_seconds < 86400 ? { hour: '2-digit' as const } : {}) }), value: metric.read(row.values) ?? 0, display: qualityValue(metric.read(row.values), metric.unit) }))} /></div>
          <h3>按{dimensions[group as keyof typeof dimensions]}对比 · {sorted.length} 组</h3>
          <div className="quality-table"><table><thead><tr><th>{dimensions[group as keyof typeof dimensions]}</th><th>{metric.label}</th><th>尝试 / 成功率样本</th><th>尝试成功率</th></tr></thead><tbody>{sorted.slice(currentPage * 20, currentPage * 20 + 20).map((row) => <tr key={row.key}>
            <td><Button variant="quiet" onClick={() => { const next = { ...filters, [group]: row.key }; setDraft(next); setFilters(next); setPage(0) }}>{groupLabel(group, row.key, row.label)}</Button></td>
            <td data-label={metric.label} className="num">{qualityValue(metric.read(row.values), metric.unit)}{metric.sample && <small>样本 {row.values[metric.sample] ?? 0}</small>}</td>
            <td data-label="尝试 / 成功率样本" className="num">{row.values.attempts} / {row.values.counted}</td><td data-label="尝试成功率" className="num">{qualityValue(qualityMetrics[1].read(row.values), '%')}</td>
          </tr>)}</tbody></table></div>
          {last > 0 && <div className="sharing-line"><Button variant="quiet" disabled={currentPage === 0} onClick={() => setPage(currentPage - 1)}>上一页</Button><span>{currentPage + 1} / {last + 1}</span><Button variant="quiet" disabled={currentPage === last} onClick={() => setPage(currentPage + 1)}>下一页</Button></div>}
          <p className="muted-copy">点击分组可筛选；缺少测量值显示「—」。</p>
        </>}
        <details><summary>统计口径</summary><p>按当前可见渠道及当前供应商归属统计，以调用开始时间归入区间，包含失败后回退的尝试。成功率 = 成功尝试 / 有效尝试，请求错误与客户端断开单列为不计入样本。供应商按原始样本汇总，不平均渠道成功率或分位数。</p><p>首响应仅统计成功的流式尝试；耗时统计所有已测量的尝试；输出速度和最大帧间隔仅取最终成功流式尝试的测量值。最大帧间隔分位数表示各次调用最大停顿的分布。少量样本的分位数参考价值有限。</p><p>前期：{formatDateTime(report.previous_from)} — {formatDateTime(report.previous_to)}。时间筛选最长 90 天，未完成调用的尝试将在落库后计入。</p></details>
      </>
    }}</QueryBoundary>
  </Card>
}
