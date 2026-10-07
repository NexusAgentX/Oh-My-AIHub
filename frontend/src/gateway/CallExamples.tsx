import { useState } from 'react'
import type { ChannelProtocol } from '../api/contracts'
import { Button, Icon, Segmented } from '../ui'
import {
  buildCallExample,
  exampleLanguages,
  type ExampleLanguage,
} from './exampleBuilder'
import { apiBaseURL } from './presentation'

/** 复制到剪贴板的按钮：短暂显示「已复制」。 */
export function CopyButton({
  text,
  label = '复制',
  variant = 'secondary',
}: {
  text: string
  label?: string
  variant?: 'secondary' | 'quiet'
}) {
  const [copied, setCopied] = useState(false)
  return (
    <Button
      icon={<Icon name={copied ? 'check' : 'copy'} />}
      onClick={() => {
        void navigator.clipboard.writeText(text).then(() => {
          setCopied(true)
          window.setTimeout(() => setCopied(false), 1800)
        })
      }}
      size="sm"
      type="button"
      variant={variant}
    >
      {copied ? '已复制' : label}
    </Button>
  )
}

/** 完整 Key 的一次性展示：等宽文本 + 复制。 */
export function SecretValue({ secret }: { secret: string }) {
  return (
    <div className="secret-value">
      <code className="mono">{secret}</code>
      <CopyButton label="复制 Key" text={secret} />
    </div>
  )
}

/**
 * 原生协议调用示例（curl / Python / Node.js）。
 * apiKey 为空时使用占位符，用于 Key 详情页中不再持有完整 Key 的场景。
 */
export function CallExamples({
  protocol,
  modelID,
  apiKey,
}: {
  protocol: ChannelProtocol
  modelID: string
  apiKey: string
}) {
  const [language, setLanguage] = useState<ExampleLanguage>('curl')
  const text = buildCallExample(language, {
    protocol,
    modelID,
    baseURL: apiBaseURL(),
    apiKey: apiKey || 'YOUR_API_KEY',
  })
  return (
    <div className="call-examples">
      <div className="call-examples-bar">
        <Segmented
          label="示例语言"
          onChange={setLanguage}
          options={exampleLanguages}
          value={language}
        />
        <CopyButton label="复制代码" text={text} />
      </div>
      <pre className="code-block" tabIndex={0}>
        <code>{text}</code>
      </pre>
    </div>
  )
}
