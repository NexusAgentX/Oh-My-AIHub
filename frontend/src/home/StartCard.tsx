import { useState } from 'react'
import { formatLabels } from '../calls'
import { useModels } from '../models/queries'
import { fetchKeySecret } from '../keys/queries'
import { errorMessage } from '../api/query'
import { useHome } from './queries'
import { Button, ButtonLink, Card, CopyButton, CopyField, Icon, InlineError, Tabs, type TabItem } from '../ui'
import { cherryStudioFields, curlSample, modelsByFormat, type SampleTab } from './codeSamples'

/** 「开始调用」：接口地址、默认 Key 与按格式的示例。 */
export function StartCard() {
  const home = useHome()
  const defaultKey = home.data?.default_key ?? null
  const origin = typeof window === 'undefined' ? '' : window.location.origin
  const endpoint = `${origin}/v1`
  const models = useModels()
  const [secret, setSecret] = useState<string | null>(null)
  const [revealError, setRevealError] = useState('')
  const [revealing, setRevealing] = useState(false)
  const available = modelsByFormat(models.data?.items ?? [])
  const formatTabs = (Object.keys(formatLabels) as Array<keyof typeof formatLabels>).filter((format) => available[format])
  const tabs: TabItem<SampleTab>[] = [
    ...formatTabs.map((format) => ({ key: format as SampleTab, label: formatLabels[format] })),
    ...(formatTabs.length > 0 ? [{ key: 'cherry' as SampleTab, label: 'Cherry Studio' }] : []),
  ]
  const [tab, setTab] = useState<SampleTab | null>(null)
  const active = tab && tabs.some((item) => item.key === tab) ? tab : tabs[0]?.key

  const getSecret = async () => {
    if (secret) return secret
    if (!defaultKey) throw new Error('no key')
    const value = await fetchKeySecret(defaultKey.id)
    setSecret(value)
    return value
  }
  const reveal = async () => {
    setRevealError('')
    setRevealing(true)
    try {
      await getSecret()
    } catch (caught) {
      setRevealError(errorMessage(caught, '读取 Key 失败，请重试'))
    } finally {
      setRevealing(false)
    }
  }

  return (
    <Card className="start-card" title="开始调用">
      <div className="start-grid">
        <CopyField label="接口地址" value={endpoint} />
        {home.isPending ? (
          <div className="copy-field">
            <span className="copy-field-label">默认 Key</span>
            <span className="muted">正在加载</span>
          </div>
        ) : home.isError ? (
          <div className="copy-field">
            <span className="copy-field-label">默认 Key</span>
            <div className="inline-error start-key-error" role="alert">
              <span>{errorMessage(home.error, '读取 Key 失败')}</span>
              <Button onClick={() => void home.refetch()} size="sm" type="button" variant="secondary">
                重试
              </Button>
            </div>
          </div>
        ) : defaultKey ? (
          <div className="copy-field">
            <span className="copy-field-label">{defaultKey.name === '默认 Key' ? '默认 Key' : `默认 Key · ${defaultKey.name}`}</span>
            <div className="copy-field-row">
              <code className={`copy-field-value ${secret ? '' : 'copy-field-masked'}`}>
                {secret ?? `${defaultKey.prefix}••••••••`}
              </code>
              {!secret && (
                <Button
                  icon={<Icon name="eye" />}
                  loading={revealing}
                  onClick={() => void reveal()}
                  size="sm"
                  type="button"
                  variant="quiet"
                >
                  显示
                </Button>
              )}
              <CopyButton value={getSecret} />
            </div>
          </div>
        ) : (
          <div className="copy-field">
            <span className="copy-field-label">API Key</span>
            <ButtonLink icon={<Icon name="plus" />} size="sm" to="/keys">
              新建 Key
            </ButtonLink>
          </div>
        )}
      </div>
      <InlineError>{revealError}</InlineError>
      {active ? (
        <Tabs items={tabs} label="调用示例" onChange={setTab} value={active}>
          {active === 'cherry' ? (
            <dl className="cherry-fields">
              {cherryStudioFields(origin, secret, available.openai_chat ?? Object.values(available)[0]).map((field) => (
                <div key={field.label}>
                  <dt>{field.label}</dt>
                  <dd>
                    <code>{field.value}</code>
                  </dd>
                </div>
              ))}
            </dl>
          ) : (
            <div className="code-sample">
              <pre>
                <code>{curlSample(active, origin, secret, available[active] ?? '')}</code>
              </pre>
              <div className="code-sample-copy">
                <CopyButton
                  value={async () => curlSample(active, origin, defaultKey ? await getSecret() : null, available[active] ?? '')}
                  variant="quiet"
                />
              </div>
            </div>
          )}
        </Tabs>
      ) : (
        models.isError && <p className="muted-copy">{errorMessage(models.error, '模型列表加载失败')}</p>
      )}
    </Card>
  )
}
