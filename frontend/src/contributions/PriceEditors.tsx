import { useEffect, useState } from 'react'
import type { components } from '../api/schema.gen'
import { Button, Checkbox, Disclosure, InlineError, SelectField, TextareaField, TextField } from '../ui'

type Schema = components['schemas']
type Prices = Schema['ContributionTokenPrices']
type Quote = Schema['ContributionQuote']
export const priceNames = { input: '输入单价', output: '输出单价', cache_write: '缓存写入单价', cache_read: '缓存读取单价' }
const modalities = { text: '文本', image: '图片', audio: '音频', video: '视频' }
const detailNames: Record<string, string> = Object.fromEntries([
  ...Object.entries({ input: '输入', output: '输出', cache_read: '缓存读取' }).flatMap(([prefix, name]) => Object.entries(modalities).map(([suffix, label]) => [`${prefix}_${suffix}`, `${name} · ${label}`])),
  ['cache_write_5m', '缓存写入 · 5 分钟'], ['cache_write_1h', '缓存写入 · 1 小时'],
])
const unitNames = { request: '每次请求', successful_image: '每张成功图片', tool_call: '每次工具调用', tool_session: '每个工具会话', tool_second: '每秒工具使用', tool_query: '每次工具查询' }

export function RawJSONField<T>({ label, value, onChange, onInvalid, validate }: { label: string; value: T; onChange: (value: T) => void; onInvalid: (bad: boolean) => void; validate?: (value: unknown) => boolean }) {
  const serialized = JSON.stringify(value, null, 2)
  const [text, setText] = useState(serialized), [error, setError] = useState(''), [focused, setFocused] = useState(false)
  useEffect(() => { if (!focused && !error) setText(serialized) }, [serialized, focused, error])
  return <Disclosure title={`高级·${label}原始 JSON`} lazy><TextareaField label={`${label}原始 JSON`} rows={8} className="contribution-json" value={text} onFocus={() => setFocused(true)} onBlur={() => setFocused(false)} onChange={event => {
    setText(event.target.value)
    try { const next = JSON.parse(event.target.value) as T; if (!next || typeof next !== 'object' || Array.isArray(value) !== Array.isArray(next) || (validate && !validate(next))) throw Error(); onChange(next); setError(''); onInvalid(false) }
    catch { setError('请输入格式与字段类型正确的 JSON'); onInvalid(true) }
  }} /><InlineError>{error}</InlineError></Disclosure>
}

function DetailPrices({ value, onChange }: { value: Prices['details']; onChange: (value: Prices['details']) => void }) {
  const entries = Object.entries(value), available = Object.keys(detailNames).filter(key => !(key in value))
  const replace = (index: number, key: string, price: string) => onChange(Object.fromEntries(entries.map((entry, i) => i === index ? [key, price] : entry)))
  return <Disclosure title="模态与缓存细分价" changed={entries.length}>
    <div className="stack-form">{entries.map(([key, price], index) => <div className="price-detail-row" key={index}><SelectField label="计价项目" value={key} onChange={event => replace(index, event.target.value, price)}>{[key, ...available].map(item => <option key={item} value={item}>{detailNames[item] || item}</option>)}</SelectField><TextField label="单价" inputMode="decimal" value={price} placeholder="免费请填写 0" onChange={event => replace(index, key, event.target.value)} /><Button type="button" variant="quiet" onClick={() => onChange(Object.fromEntries(entries.filter((_, i) => i !== index)))}>移除</Button></div>)}<Button type="button" variant="secondary" disabled={!available.length} onClick={() => onChange({ ...value, [available[0]]: '' })}>添加细分价</Button></div>
  </Disclosure>
}
function TokenPrices({ value, onChange }: { value: Prices; onChange: (value: Prices) => void }) {
  return <><div className="field-row">{(Object.keys(priceNames) as (keyof typeof priceNames)[]).map(key => <TextField key={key} inputMode="decimal" label={priceNames[key]} value={value[key]} placeholder="免费请填写 0" onChange={event => onChange({ ...value, [key]: event.target.value })} />)}</div><DetailPrices value={value.details} onChange={details => onChange({ ...value, details })} /></>
}
export function formatMinute(value: number | null) { return value === null ? '' : `${String(Math.floor(value / 60)).padStart(2, '0')}:${String(value % 60).padStart(2, '0')}` }
function TimeField({ label, value, onChange, end = false }: { label: string; value: number | null; onChange: (value: number | null) => void; end?: boolean }) {
  return <div className="stack-form"><TextField type="time" label={label} value={value === 1440 ? '' : formatMinute(value)} disabled={value === 1440} onChange={event => { const [hours, minutes] = event.target.value.split(':').map(Number); onChange(event.target.value ? hours * 60 + minutes : null) }} />{end && <Checkbox label="当天 24:00" checked={value === 1440} onChange={event => onChange(event.target.checked ? 1440 : null)} />}</div>
}
const emptyPrices = (): Prices => ({ input: '', output: '', cache_write: '', cache_read: '', details: {} })
const emptyConditions = (): Schema['ContributionPriceConditions'] => ({ service_tier: '', min_prompt_tokens: null, max_prompt_tokens: null, timezone: '', weekdays: [], start_minute: null, end_minute: null })
function record(value: unknown): value is Record<string, unknown> { return Boolean(value) && typeof value === 'object' && !Array.isArray(value) }
function tokenPrices(value: unknown) { return record(value) && Object.keys(priceNames).every(key => typeof value[key] === 'string') && record(value.details) && Object.keys(value.details).length <= 16 && Object.values(value.details).every(price => typeof price === 'string') }
export function validRawQuote(value: unknown) {
  if (!record(value) || !tokenPrices(value.prices) || !Array.isArray(value.tiers) || value.tiers.length > 16 || !Array.isArray(value.unit_prices) || value.unit_prices.length > 128) return false
  return value.tiers.every(tier => {
    if (!record(tier) || typeof tier.name !== 'string' || !tokenPrices(tier.prices) || !record(tier.conditions)) return false
    const conditions = tier.conditions
    return ['service_tier', 'timezone'].every(key => typeof conditions[key] === 'string') && ['min_prompt_tokens', 'max_prompt_tokens'].every(key => conditions[key] === null || Number.isSafeInteger(conditions[key]) && Number(conditions[key]) >= 0) && ['start_minute', 'end_minute'].every(key => conditions[key] === null || Number.isInteger(conditions[key]) && Number(conditions[key]) >= (key === 'end_minute' ? 1 : 0) && Number(conditions[key]) <= (key === 'end_minute' ? 1440 : 1439)) && Array.isArray(conditions.weekdays) && conditions.weekdays.every(day => Number.isInteger(day) && day >= 1 && day <= 7)
  }) && value.unit_prices.every(unit => record(unit) && typeof unit.unit === 'string' && Object.hasOwn(unitNames, unit.unit) && ['unit', 'tool', 'image_size', 'image_quality', 'image_background', 'service_tier', 'price'].every(key => typeof unit[key] === 'string'))
}
export function QuotePricesEditor({ value, onChange, onInvalid }: { value: Quote; onChange: (value: Quote) => void; onInvalid: (bad: boolean) => void }) {
  const setTier = (index: number, patch: Partial<Quote['tiers'][number]>) => onChange({ ...value, tiers: value.tiers.map((item, i) => i === index ? { ...item, ...patch } : item) })
  const setUnit = (index: number, patch: Partial<Quote['unit_prices'][number]>) => onChange({ ...value, unit_prices: value.unit_prices.map((item, i) => i === index ? { ...item, ...patch } : item) })
  return <div className="stack-form">
    {value.billing_unit === 'per_million_tokens' && <TokenPrices value={value.prices} onChange={prices => onChange({ ...value, prices })} />}
    <Disclosure title="条件价格" changed={value.tiers.length}><div className="stack-form">{value.tiers.map((tier, index) => <section className="contribution-section" key={index}><h4>条件 {index + 1}</h4><TextField label="名称" maxLength={64} value={tier.name} onChange={event => setTier(index, { name: event.target.value })} /><div className="field-row"><TextField label="服务档位（可选）" value={tier.conditions.service_tier} placeholder="如 openai:flex" onChange={event => setTier(index, { conditions: { ...tier.conditions, service_tier: event.target.value } })} />{(['min_prompt_tokens', 'max_prompt_tokens'] as const).map(key => <TextField key={key} label={key === 'min_prompt_tokens' ? '输入 token 下限（含）' : '输入 token 上限（不含）'} type="number" min={0} value={tier.conditions[key] ?? ''} onChange={event => setTier(index, { conditions: { ...tier.conditions, [key]: event.target.value === '' ? null : Number(event.target.value) } })} />)}</div><Disclosure title="星期与时段"><div className="stack-form"><TextField label="IANA 时区" value={tier.conditions.timezone} placeholder="Asia/Shanghai" onChange={event => setTier(index, { conditions: { ...tier.conditions, timezone: event.target.value } })} /><div className="price-weekdays">{['一', '二', '三', '四', '五', '六', '日'].map((label, day) => <Checkbox key={day} label={`周${label}`} checked={tier.conditions.weekdays.includes(day + 1)} onChange={event => setTier(index, { conditions: { ...tier.conditions, weekdays: event.target.checked ? [...tier.conditions.weekdays, day + 1].sort() : tier.conditions.weekdays.filter(item => item !== day + 1) } })} />)}</div><div className="field-row"><TimeField label="开始时间" value={tier.conditions.start_minute} onChange={start_minute => setTier(index, { conditions: { ...tier.conditions, start_minute } })} /><TimeField end label="结束时间" value={tier.conditions.end_minute} onChange={end_minute => setTier(index, { conditions: { ...tier.conditions, end_minute } })} /></div></div></Disclosure><TokenPrices value={tier.prices} onChange={prices => setTier(index, { prices })} /><Button type="button" variant="quiet" onClick={() => onChange({ ...value, tiers: value.tiers.filter((_, i) => i !== index) })}>移除此条件价</Button></section>)}<Button type="button" variant="secondary" disabled={value.tiers.length >= 16} onClick={() => onChange({ ...value, tiers: [...value.tiers, { name: '', conditions: emptyConditions(), prices: emptyPrices() }] })}>添加条件价</Button></div></Disclosure>
    <Disclosure title="按次、图片与工具单价" changed={value.unit_prices.length} defaultOpen={value.billing_unit !== 'per_million_tokens'}><div className="stack-form">{value.unit_prices.map((unit, index) => <section className="contribution-section" key={index}><div className="field-row"><SelectField label="计价项目" value={unit.unit} onChange={event => { const next = event.target.value as typeof unit.unit; setUnit(index, { unit: next, ...(next.startsWith('tool_') ? {} : { tool: '' }) }) }}>{Object.entries(unitNames).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</SelectField><TextField label="单价" inputMode="decimal" placeholder="免费请填写 0" value={unit.price} onChange={event => setUnit(index, { price: event.target.value })} /></div>{unit.unit.startsWith('tool_') && <TextField label="工具名称" value={unit.tool} placeholder="如 web_search" onChange={event => setUnit(index, { tool: event.target.value })} />}<Disclosure title="匹配条件"><div className="field-row">{(['image_size', 'image_quality', 'image_background', 'service_tier'] as const).map(key => <TextField key={key} label={{ image_size: '图片尺寸', image_quality: '图片质量', image_background: '图片背景', service_tier: '服务档位' }[key]} value={unit[key]} onChange={event => setUnit(index, { [key]: event.target.value })} />)}</div></Disclosure><Button type="button" variant="quiet" onClick={() => onChange({ ...value, unit_prices: value.unit_prices.filter((_, i) => i !== index) })}>移除此单价</Button></section>)}<Button type="button" variant="secondary" disabled={value.unit_prices.length >= 128} onClick={() => onChange({ ...value, unit_prices: [...value.unit_prices, { unit: value.billing_unit === 'per_request' ? 'request' : value.billing_unit === 'per_successful_image' ? 'successful_image' : 'tool_call', tool: '', image_size: '', image_quality: '', image_background: '', service_tier: '', price: '' }] })}>添加单价</Button></div></Disclosure>
    <RawJSONField label="报价" value={{ prices: value.prices, tiers: value.tiers, unit_prices: value.unit_prices }} validate={validRawQuote} onChange={next => onChange({ ...value, prices: next.prices, tiers: next.tiers, unit_prices: next.unit_prices })} onInvalid={onInvalid} />
  </div>
}
