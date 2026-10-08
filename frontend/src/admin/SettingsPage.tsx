import { useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import { Button, Card, InlineError, PageHeader, QueryBoundary, SuccessMessage, TextareaField, TextField } from '../ui'
import { Collapsible } from './components'
import { formatDateTime } from './format'
import { useAdminSettings, useUpdateSettings } from './queries'
import {
  changedAdvancedCount,
  formToSettings,
  settingsToForm,
  type SettingsErrors,
  type SettingsForm,
} from './settingsForm'
import type { Settings } from './types'

function SettingsEditor({ settings }: { settings: Settings }) {
  const [form, setForm] = useState<SettingsForm>(() => settingsToForm(settings))
  const [errors, setErrors] = useState<SettingsErrors>({})
  const [message, setMessage] = useState('')
  const update = useUpdateSettings()
  const set = (key: keyof SettingsForm) => (event: { target: { value: string } }) => {
    setForm((current) => ({ ...current, [key]: event.target.value }))
    setMessage('')
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const result = formToSettings(form)
    if (result.errors) {
      setErrors(result.errors)
      return
    }
    setErrors({})
    update.mutate(result.body, {
      onSuccess: (data) => {
        setForm(settingsToForm(data.settings))
        setMessage('已保存')
      },
    })
  }

  const field = (key: keyof SettingsForm, label: string, props: Record<string, unknown> = {}) => (
    <TextField
      error={errors[key]}
      inputMode="decimal"
      label={label}
      onChange={set(key)}
      value={form[key]}
      {...props}
    />
  )

  return (
    <form className="stack-form" noValidate onSubmit={submit}>
      <div className="field-row">
        {field('feePercent', '手续费率（%）')}
        {field('paymentTimeoutMinutes', 'C2C 付款超时（分钟）', { inputMode: 'numeric' })}
      </div>
      <Collapsible changed={changedAdvancedCount(form)} title="高级设置">
        <div className="stack-form">
          <div className="field-row">
            {field('defaultCreditLimit', '新用户默认信用额度（积分）')}
            {field('defaultMaxAttempts', '默认最多尝试渠道数', { inputMode: 'numeric' })}
          </div>
          <div className="field-row">
            {field('defaultTtftSeconds', '默认首字超时（秒）', { inputMode: 'numeric' })}
            {field('defaultTotalSeconds', '默认总超时（秒）', { inputMode: 'numeric' })}
          </div>
          <div className="field-row">
            {field('defaultCooldownFailures', '冷却：连续失败次数', { inputMode: 'numeric' })}
            {field('defaultCooldownSeconds', '冷却时长（秒）', { inputMode: 'numeric' })}
          </div>
          <TextareaField
            error={errors.extraBlockedHosts}
            hint="每行一个，叠加在部署配置的禁用清单之上"
            label="额外禁用的上游主机"
            onChange={set('extraBlockedHosts')}
            rows={4}
            value={form.extraBlockedHosts}
          />
        </div>
      </Collapsible>
      <InlineError>{update.isError ? errorMessage(update.error, '保存失败，请重试') : ''}</InlineError>
      <SuccessMessage>{message}</SuccessMessage>
      <div className="form-actions">
        <span className="muted-copy form-actions-note">更新于 {formatDateTime(settings.updated_at)}</span>
        <Button loading={update.isPending}>保存</Button>
      </div>
    </form>
  )
}

export function SettingsPage() {
  const settings = useAdminSettings()
  return (
    <>
      <PageHeader title="设置" />
      <Card>
        <QueryBoundary query={settings}>
          {(data) => <SettingsEditor settings={data.settings} />}
        </QueryBoundary>
      </Card>
    </>
  )
}
