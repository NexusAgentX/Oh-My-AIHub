import { useState } from 'react'
import type { components } from '../api/schema.gen'
import { Button, InlineError, TextareaField, TextField } from '../ui'

type Config = components['schemas']['CodexSettings']
export const emptyCodex: Config = { whoami: '', accounts: '', models: '' }
function parse(raw: string): Record<string, unknown> | null {
  if (!raw) return null
  const value: unknown = JSON.parse(raw)
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('请输入 JSON 对象')
  return value as Record<string, unknown>
}
function models(raw: string): Array<Record<string, unknown>> {
  const value = parse(raw)
  return Array.isArray(value?.models) ? value.models.filter((v): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v)) : []
}
export function CodexEditor({ value, saved, onChange }: {value: Config; saved: Config; onChange: (value: Config) => void}) {
  const [errors, setErrors] = useState<Partial<Record<keyof Config, string>>>({})
  const [preview, setPreview] = useState(false)
  const [search, setSearch] = useState('')
  const fields: Array<[keyof Config, string]> = [['whoami', 'PAT 身份响应'], ['accounts', '账号检查响应'], ['models', '模型目录']]
  const check = (field: keyof Config, format = false) => {
    try {
      const parsed = parse(value[field])
      setErrors(current => ({...current, [field]: ''}))
      if (format && parsed) onChange({...value, [field]: JSON.stringify(parsed, null, 2)})
    } catch (error) { setErrors(current => ({...current, [field]: error instanceof Error ? error.message : 'JSON 无效'})) }
  }
  let rows: Array<Record<string, unknown>> = []
  let before: Array<Record<string, unknown>> = []
  try { rows = models(value.models); before = models(saved.models) } catch { /* editor reports the error on validation */ }
  const old = new Map(before.map(v => [v.slug, v]))
  const current = new Set(rows.map(v => v.slug))
  const added = rows.filter(v => !old.has(v.slug)).length
  const changed = rows.filter(v => old.has(v.slug) && JSON.stringify(old.get(v.slug)) !== JSON.stringify(v)).length
  const removed = before.filter(v => !current.has(v.slug)).length
  return <fieldset className="form-section codex-editor">
    <legend>Codex 兼容响应</legend>
    <p className="muted-copy">留空关闭对应接口；所有有效 Key 共用。粘贴前移除个人信息和凭据。</p>
    {fields.map(([field, label]) => <details key={field} open={field === 'models'}>
      <summary>{label}{value[field] !== saved[field] ? ' · 未保存' : ''}</summary>
      <TextareaField className="mono codex-json" label={`${label} JSON`} spellCheck={false} rows={field === 'models' ? 12 : 6} value={value[field]} error={errors[field]} onBlur={() => check(field)} onChange={event => onChange({...value, [field]: event.target.value})} />
      <div className="form-actions">
        <Button type="button" variant="secondary" onClick={() => check(field)}>校验 JSON</Button>
        <Button type="button" variant="secondary" onClick={() => check(field, true)}>格式化</Button>
      </div>
    </details>)}
    <div className="form-actions"><Button type="button" variant="secondary" onClick={() => setPreview(v => !v)}>{preview ? '收起预览' : '预览与变更'}</Button><span>新增 {added} · 删除 {removed} · 修改 {changed}</span></div>
    {preview && <>
      <TextField label="搜索模型与字段" value={search} onChange={event => setSearch(event.target.value)} />
      {rows.filter(row => JSON.stringify(row).toLowerCase().includes(search.toLowerCase())).map(row => <details key={String(row.slug)}><summary>{String(row.slug)} · 窗口 {String(row.context_window ?? '—')}{!old.has(row.slug) ? ' · 新增' : JSON.stringify(old.get(row.slug)) !== JSON.stringify(row) ? ' · 修改' : ''}</summary><div className="codex-diff"><div>已保存<pre>{JSON.stringify(old.get(row.slug) ?? null, null, 2)}</pre></div><div>待保存<pre>{JSON.stringify(row, null, 2)}</pre></div></div></details>)}
      {before.filter(row => !current.has(row.slug)).map(row => <p key={String(row.slug)}>删除：{String(row.slug)}</p>)}
      <InlineError>{value.models && rows.length === 0 ? '未读到模型，请检查 models 数组。' : ''}</InlineError>
    </>}
  </fieldset>
}
