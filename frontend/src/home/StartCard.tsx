import { useState } from 'react'
import { fetchKeySecret } from '../keys/queries'
import { errorMessage } from '../api/query'
import { useHome } from './queries'
import { Button, ButtonLink, Card, CopyButton, CopyField, Icon, InlineError } from '../ui'

/** 「开始调用」：接口地址与默认 Key。 */
export function StartCard() {
  const home = useHome()
  const defaultKey = home.data?.default_key ?? null
  const origin = typeof window === 'undefined' ? '' : window.location.origin
  const endpoint = `${origin}/v1`
  const [secret, setSecret] = useState<string | null>(null)
  const [revealError, setRevealError] = useState('')
  const [revealing, setRevealing] = useState(false)

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
    </Card>
  )
}
