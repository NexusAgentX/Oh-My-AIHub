import type { Overrides } from './queries'
import { Badge, Button, Disclosure, Icon, IconButton, InlineError, Segmented, TextField } from '../ui'
import { InheritedField } from '../ui/InheritedField'
import { useFormDefaults } from '../settings/queries'

type NumberKey = Exclude<keyof Overrides, 'user_agent' | 'header_rules'>
export const numericFields: { key: NumberKey; label: string; min: number; max: number }[] = [
  { key: 'concurrency_limit', label: '并发上限', min: 1, max: 1073741824 }, { key: 'rpm_limit', label: '每分钟请求数', min: 1, max: 1073741824 },
  { key: 'ttft_timeout_ms', label: '首字超时（毫秒）', min: 1000, max: 600000 }, { key: 'request_timeout_ms', label: '请求超时（毫秒）', min: 1000, max: 3600000 }, { key: 'idle_timeout_ms', label: '空闲超时（毫秒）', min: 1000, max: 3600000 },
  { key: 'cooldown_failures', label: '连续失败次数', min: 1, max: 100 }, { key: 'cooldown_seconds', label: '冷却时长（秒）', min: 10, max: 86400 },
]
export type HeaderMode = 'inherit' | 'empty' | 'custom'
export function headerMode(rules: Overrides['header_rules']): HeaderMode { return rules === null ? 'inherit' : rules.set?.length || rules.remove?.length ? 'custom' : 'empty' }
export function Advanced({ value, defaults, onChange, mode, onModeChange, defaultsKnown = true }: { value: Overrides; defaults: Overrides; onChange: (v: Overrides) => void; mode: HeaderMode; defaultsKnown?: boolean; onModeChange: (mode: HeaderMode) => void }) {
  const platform = useFormDefaults()
  const set = (patch: Partial<Overrides>) => onChange({ ...value, ...patch })
  const rules = value.header_rules ?? {}
  return <Disclosure title="高级设置" changed={Object.values(value).filter(item => item !== null).length}><div className="group-advanced-fields">
    <InheritedField label="User-Agent" value={value.user_agent} inherited={!defaultsKnown ? '分组默认值暂不可用' : defaults.user_agent ?? '平台默认 User-Agent'} source={defaults.user_agent === null ? '平台默认' : '继承·分组'} maxLength={512} onRestore={() => set({ user_agent: null })} onChange={event => set({ user_agent: event.target.value || null })} />
    <div className="field-row">{numericFields.map(({ key, label, min, max }) => {
      const platformKey = `default_${key}` as keyof NonNullable<typeof platform.data>
      const fallback = platform.data?.[platformKey]
      const unit = key.endsWith('_ms') ? ' 毫秒' : key === 'cooldown_seconds' ? ' 秒' : ''
      const inherited = defaults[key] ?? fallback
      const source = defaults[key] !== null ? '继承·分组' : key.endsWith('_timeout_ms') ? '继承·模型路由／平台' : '平台默认'
      return <InheritedField key={key} label={label} type="number" min={min} max={max} step={1} value={value[key]} inherited={!defaultsKnown ? '分组默认值暂不可用' : inherited !== undefined && inherited !== null ? `${inherited}${unit}` : key === 'rpm_limit' || key === 'concurrency_limit' ? '不限' : platform.isError ? '默认值加载失败' : '加载默认值…'} source={defaultsKnown ? source : '继承·分组'} hint={key.endsWith('_timeout_ms') && defaults[key] === null ? '显示平台兜底值；请求的模型路由可另设，渠道覆盖优先。' : undefined} onRestore={() => set({ [key]: null })} onChange={event => set({ [key]: event.target.value === '' ? null : Number(event.target.value) })} />
    })}</div>
    <div className="stack-form"><Segmented label="请求头规则" value={mode} options={[{ key: 'inherit', label: '继承分组' }, { key: 'empty', label: '明确不设' }, { key: 'custom', label: '自定义' }]} onChange={next => { onModeChange(next); set({ header_rules: next === 'inherit' ? null : next === 'empty' ? {} : value.header_rules ?? {} }) }} /><Badge tone={mode === 'inherit' ? 'neutral' : 'info'}>{mode === 'inherit' ? '继承·分组' : '已覆盖'}</Badge></div>
    {mode === 'inherit' && !defaultsKnown && <p className="muted-copy">分组请求头规则暂不可用</p>}
    {mode === 'inherit' && defaultsKnown && <div className="muted-copy"><strong>分组规则</strong>{defaults.header_rules?.set?.map(rule => <p key={rule.name}>{rule.name}: {rule.value}</p>)}{defaults.header_rules?.remove?.map(name => <p key={name}>删除 {name}</p>)}{!defaults.header_rules?.set?.length && !defaults.header_rules?.remove?.length && <p>未设置</p>}</div>}
    {mode === 'custom' && <div className="group-header-editor">
      {!rules.set?.length && !rules.remove?.length && <InlineError>请添加规则，或选择明确不设。</InlineError>}
      {(rules.set ?? []).map((rule, index) => <div className="header-rule" key={index}><TextField label="请求头名称" required value={rule.name} onChange={e => set({ header_rules: { ...rules, set: rules.set?.map((r, i) => i === index ? { ...r, name: e.target.value } : r) } })} /><TextField label="请求头值" value={rule.value} onChange={e => set({ header_rules: { ...rules, set: rules.set?.map((r, i) => i === index ? { ...r, value: e.target.value } : r) } })} /><IconButton type="button" label="移除设置规则" icon={<Icon name="x" />} onClick={() => set({ header_rules: { ...rules, set: rules.set?.filter((_, i) => i !== index) } })} /></div>)}
      {(rules.remove ?? []).map((name, index) => <div className="header-rule header-rule-remove" key={index}><TextField label="要删除的请求头" required value={name} onChange={e => set({ header_rules: { ...rules, remove: rules.remove?.map((r, i) => i === index ? e.target.value : r) } })} /><IconButton type="button" label="移除删除规则" icon={<Icon name="x" />} onClick={() => set({ header_rules: { ...rules, remove: rules.remove?.filter((_, i) => i !== index) } })} /></div>)}
      <div className="group-channel-actions"><Button size="sm" type="button" variant="secondary" disabled={(rules.set?.length ?? 0) >= 32} onClick={() => set({ header_rules: { ...rules, set: [...(rules.set ?? []), { name: '', value: '' }] } })}>设置请求头</Button><Button size="sm" type="button" variant="secondary" disabled={(rules.remove?.length ?? 0) >= 32} onClick={() => set({ header_rules: { ...rules, remove: [...(rules.remove ?? []), ''] } })}>删除请求头</Button></div>
    </div>}
  </div></Disclosure>
}
