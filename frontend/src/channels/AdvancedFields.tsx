import { Button, Icon, IconButton, TextField } from '../ui'
import type { AdvancedForm } from './channelForm'

/** 渠道高级设置：留空即平台默认。 */
export function AdvancedFields({ form, onChange }: { form: AdvancedForm; onChange: (form: AdvancedForm) => void }) {
  const set = (patch: Partial<AdvancedForm>) => onChange({ ...form, ...patch })
  const int = (label: string, key: keyof AdvancedForm, hint?: string) => (
    <TextField
      hint={hint}
      inputMode="numeric"
      label={label}
      min={1}
      onChange={(event) => set({ [key]: event.target.value } as Partial<AdvancedForm>)}
      placeholder="默认"
      type="number"
      value={form[key] as string}
    />
  )
  return (
    <>
      <TextField label="User-Agent" onChange={(event) => set({ userAgent: event.target.value })} placeholder="不修改" value={form.userAgent} />
      <fieldset className="form-section">
        <legend>请求头规则</legend>
        {form.headerSet.map((row, index) => (
          // 规则行没有稳定 ID，按位置作为 key
          <div className="header-rule" key={index}>
            <span className="badge badge-info">设置</span>
            <input
              aria-label="请求头名称"
              className="input mono"
              onChange={(event) => set({ headerSet: form.headerSet.map((item, position) => (position === index ? { ...item, name: event.target.value } : item)) })}
              placeholder="名称"
              value={row.name}
            />
            <input
              aria-label="请求头值"
              className="input mono"
              onChange={(event) => set({ headerSet: form.headerSet.map((item, position) => (position === index ? { ...item, value: event.target.value } : item)) })}
              placeholder="值"
              value={row.value}
            />
            <IconButton icon={<Icon name="x" />} label="删除规则" onClick={() => set({ headerSet: form.headerSet.filter((_, position) => position !== index) })} />
          </div>
        ))}
        {form.headerRemove.map((name, index) => (
          <div className="header-rule header-rule-remove" key={`remove-${index}`}>
            <span className="badge badge-warning">删除</span>
            <input
              aria-label="要删除的请求头"
              className="input mono"
              onChange={(event) => set({ headerRemove: form.headerRemove.map((item, position) => (position === index ? event.target.value : item)) })}
              placeholder="名称"
              value={name}
            />
            <IconButton icon={<Icon name="x" />} label="删除规则" onClick={() => set({ headerRemove: form.headerRemove.filter((_, position) => position !== index) })} />
          </div>
        ))}
        <div className="header-rule-actions">
          <Button icon={<Icon name="plus" />} onClick={() => set({ headerSet: [...form.headerSet, { name: '', value: '' }] })} size="sm" type="button" variant="secondary">
            设置请求头
          </Button>
          <Button icon={<Icon name="plus" />} onClick={() => set({ headerRemove: [...form.headerRemove, ''] })} size="sm" type="button" variant="secondary">
            删除请求头
          </Button>
        </div>
      </fieldset>
      <div className="field-row field-row-three">
        {int('并发上限', 'concurrency')}
        {int('每分钟请求数', 'rpm')}
        <TextField inputMode="decimal" label="每日收入上限" onChange={(event) => set({ dailyCap: event.target.value.trim() })} placeholder="不限" value={form.dailyCap} />
      </div>
      <div className="field-row">
        {int('首字超时（毫秒）', 'ttftTimeout')}
        {int('总超时（毫秒）', 'totalTimeout')}
      </div>
      <div className="field-row">
        {int('连续失败次数', 'cooldownFailures', '达到后进入冷却')}
        {int('冷却时长（分钟）', 'cooldownMinutes')}
      </div>
    </>
  )
}
