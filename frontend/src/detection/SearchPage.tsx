import { useEffect, useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import { Button, Card, InlineError, PageHeader, QueryBoundary, SelectField, SuccessMessage, TextField } from '../ui'
import { useDetectionParameters, useUpdateDetectionSearch, type SearchSettings } from './queries'
import './detection.css'
function Editor({ target, value }: { target: 'context' | 'max_output'; value: SearchSettings }) {
  const [form, setForm] = useState(value)
  const [message, setMessage] = useState('')
  useEffect(() => { setForm(value) }, [value])
  const update = useUpdateDetectionSearch()
  const numeric = (field: 'low' | 'high' | 'hard_maximum' | 'precision' | 'rounds' | 'rechecks', label: string, step = 1) => <TextField label={label} type="number" step={step} min={field === 'rounds' || field === 'precision' ? step : 0} value={form[field]} required onChange={(e) => { setMessage(''); setForm((old) => ({ ...old, [field]: Number(e.target.value) })) }} />
  const valid = Number.isFinite(form.precision) && form.precision > 0 && form.low >= 0 && form.low < form.high && form.high <= form.hard_maximum && form.rounds >= 1 && form.rounds <= 128 && Number.isInteger(form.rounds) && form.rechecks >= 0 && form.rechecks <= 8 && Number.isInteger(form.rechecks) && (form.precision_mode !== 'relative' || form.precision <= 1)
  const submit = (event: FormEvent) => { event.preventDefault(); if (!valid) return; update.mutate({ target, value: form }, { onSuccess: ({ settings }) => { setForm(settings); setMessage('已保存，后续新任务使用此版本') } }) }
  return <Card title={target === 'context' ? '上下文长度' : '最大输出'}><form className="stack-form" onSubmit={submit}>
    <div className="field-row"><SelectField label="搜索方法" value={form.method} onChange={(e) => setForm((old) => ({ ...old, method: e.target.value as SearchSettings['method'] }))}><option value="binary">二分</option><option value="ternary">三分</option></SelectField><SelectField label="精度模式" value={form.precision_mode} onChange={(e) => setForm((old) => ({ ...old, precision_mode: e.target.value as SearchSettings['precision_mode'] }))}><option value="absolute">绝对 token 数</option><option value="relative">相对比例</option></SelectField></div>
    <div className="field-row">{numeric('low', '初始下界')}{numeric('high', '初始上界')}</div><div className="field-row">{numeric('hard_maximum', '单次探测硬上界')}{numeric('precision', '停止精度', form.precision_mode === 'relative' ? 0.001 : 1)}</div><div className="field-row">{numeric('rounds', '最大轮数（1–128）')}{numeric('rechecks', '复核次数（0–8）')}</div>
    <p className="muted-copy">当前版本 {value.version} · 在途任务保留原参数。</p>
    <InlineError>{!valid && '请检查区间、精度与有限轮数。'}</InlineError><InlineError>{update.isError && errorMessage(update.error, '保存失败')}</InlineError><SuccessMessage>{message}</SuccessMessage>
    <Button variant="secondary" disabled={!valid} loading={update.isPending}>保存参数</Button>
  </form></Card>
}
export function DetectionSearchPage() { const query = useDetectionParameters(); return <><PageHeader title="检测搜索参数" description="控制长度探测算法；用户自行在上游限制消费。" /><QueryBoundary query={query}>{({ parameters }) => <div className="detection-settings"><Editor target="context" value={parameters.context} /><Editor target="max_output" value={parameters.max_output} /></div>}</QueryBoundary></> }
