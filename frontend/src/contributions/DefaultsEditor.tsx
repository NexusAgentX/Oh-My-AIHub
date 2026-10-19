import { useState } from 'react'
import { Advanced, headerMode } from '../group-channels/SettingsFields'
import { emptyOverrides } from '../settings/overrides'
import type { Overrides } from '../group-channels/queries'
import { RawJSONField } from './PriceEditors'

function validDefaults(value: unknown) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const data = value as Record<string, unknown>
  if (!(data.user_agent === null || typeof data.user_agent === 'string')) return false
  for (const key of Object.keys(emptyOverrides).filter(key => key !== 'user_agent' && key !== 'header_rules')) if (data[key] !== null && typeof data[key] !== 'number') return false
  if (data.header_rules === null) return true
  if (!data.header_rules || typeof data.header_rules !== 'object' || Array.isArray(data.header_rules)) return false
  const rules = data.header_rules as Record<string, unknown>
  return (rules.set === undefined || Array.isArray(rules.set) && rules.set.every(rule => rule && typeof rule.name === 'string' && typeof rule.value === 'string')) && (rules.remove === undefined || Array.isArray(rules.remove) && rules.remove.every(name => typeof name === 'string'))
}
export function DefaultsEditor({ value, onChange, onInvalid }: { value: Overrides; onChange: (value: Overrides) => void; onInvalid: (bad: boolean) => void }) {
  const [mode, setMode] = useState(() => headerMode(value.header_rules))
  const [rawBad, setRawBad] = useState(false)
  return <><Advanced level="group" value={value} defaults={emptyOverrides} onChange={onChange} mode={mode} onModeChange={setMode} onInvalid={bad => onInvalid(bad || rawBad)} /><RawJSONField label="默认设置" value={value} validate={validDefaults} onChange={next => { setMode(headerMode(next.header_rules)); onChange(next) }} onInvalid={bad => { setRawBad(bad); onInvalid(bad) }} /></>
}
