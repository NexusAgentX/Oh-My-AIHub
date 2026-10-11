import { useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import {
  Button,
  Card,
  FormSection,
  InlineError,
  Notice,
  PageHeader,
  QueryBoundary,
  SuccessMessage,
  Switch,
  TextareaField,
  TextField,
} from '../ui'
import { Collapsible } from './components'
import { formatDateTime } from './format'
import { useAdminSettings, useSyncExchangeRate, useUpdateSettings } from './queries'
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
  const sync = useSyncExchangeRate()
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

  const field = (
    key: Exclude<keyof SettingsForm, 'fxAutoSync'>,
    label: string,
    props: Record<string, unknown> = {},
  ) => (
    <TextField
      error={errors[key]}
      inputMode="decimal"
      label={label}
      onChange={set(key)}
      value={form[key]}
      {...props}
    />
  )

  const syncNow = () => {
    setMessage('')
    sync.mutate(undefined, {
      onSuccess: (data) => {
        setForm((current) => ({ ...current, usdCnyRate: data.settings.usd_cny_rate }))
        setMessage(`汇率已同步：1 美元 = ${data.settings.usd_cny_rate} 积分`)
      },
    })
  }

  return (
    <form className="stack-form" noValidate onSubmit={submit}>
      <div className="field-row">
        {field('feePercent', '手续费率（%）')}
        {field('paymentTimeoutMinutes', 'C2C 付款超时（分钟）', { inputMode: 'numeric' })}
      </div>
      <FormSection title="美元汇率">
        <div className="field-row">
          {field('usdCnyRate', '1 美元 = ? 积分', {
            disabled: form.fxAutoSync,
            hint: form.fxAutoSync ? '自动同步中，不能手动修改' : '美元模型按此换算成积分',
          })}
          {field('fxSyncTime', '每天同步时间（北京时间）', {
            disabled: !form.fxAutoSync,
            inputMode: 'numeric',
            type: 'time',
          })}
        </div>
        <Switch
          checked={form.fxAutoSync}
          label="每天自动同步（欧洲央行参考汇率）"
          onChange={(checked) => {
            setForm((current) => ({ ...current, fxAutoSync: checked }))
            setMessage('')
          }}
        />
        {settings.fx_sync_error && (
          <Notice tone="danger">
            上次同步失败（{formatDateTime(settings.fx_sync_attempted_at)}）：{settings.fx_sync_error}
          </Notice>
        )}
        <InlineError>{sync.isError ? errorMessage(sync.error, '同步失败，请重试') : ''}</InlineError>
        <div className="form-actions">
          <span className="muted-copy form-actions-note">
            {settings.usd_cny_rate_source === 'auto' ? '自动同步' : '手动填写'}，更新于{' '}
            {formatDateTime(settings.usd_cny_rate_updated_at)}
          </span>
          <Button loading={sync.isPending} onClick={syncNow} type="button" variant="secondary">
            立即同步
          </Button>
        </div>
      </FormSection>
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
