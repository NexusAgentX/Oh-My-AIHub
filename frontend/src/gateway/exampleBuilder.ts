import type { ChannelProtocol } from '../api/types'

export type ExampleLanguage = 'curl' | 'python' | 'node'

export const exampleLanguages: Array<{ key: ExampleLanguage; label: string }> = [
  { key: 'curl', label: 'curl' },
  { key: 'python', label: 'Python' },
  { key: 'node', label: 'Node.js' },
]

export type ExampleInput = {
  protocol: ChannelProtocol
  modelID: string
  baseURL: string
  apiKey: string
}

/** 四种原生协议的外部入口路径；Gemini 的模型写在路径里。 */
export function protocolEndpoint(
  protocol: ChannelProtocol,
  modelID: string,
  baseURL: string,
) {
  const base = baseURL.replace(/\/+$/, '')
  switch (protocol) {
    case 'openai_chat_completions':
      return `${base}/v1/chat/completions`
    case 'openai_responses':
      return `${base}/v1/responses`
    case 'anthropic_messages':
      return `${base}/v1/messages`
    case 'google_gemini_generate_content':
      return `${base}/v1beta/models/${modelID}:generateContent`
  }
}

/** 原生协议的凭据与固定请求头。 */
export function protocolHeaders(protocol: ChannelProtocol, apiKey: string) {
  switch (protocol) {
    case 'openai_chat_completions':
    case 'openai_responses':
      return { Authorization: `Bearer ${apiKey}`, 'Content-Type': 'application/json' }
    case 'anthropic_messages':
      return {
        'x-api-key': apiKey,
        'anthropic-version': '2023-06-01',
        'Content-Type': 'application/json',
      }
    case 'google_gemini_generate_content':
      return { 'x-goog-api-key': apiKey, 'Content-Type': 'application/json' }
  }
}

export function protocolBody(
  protocol: ChannelProtocol,
  modelID: string,
): Record<string, unknown> {
  switch (protocol) {
    case 'openai_chat_completions':
      return { model: modelID, messages: [{ role: 'user', content: 'Hello' }] }
    case 'openai_responses':
      return { model: modelID, input: 'Hello' }
    case 'anthropic_messages':
      return {
        model: modelID,
        max_tokens: 256,
        messages: [{ role: 'user', content: 'Hello' }],
      }
    case 'google_gemini_generate_content':
      return { contents: [{ parts: [{ text: 'Hello' }] }] }
  }
}

/** 顶层键逐行展示，嵌套值保持单行，示例更紧凑。 */
function formatBody(body: Record<string, unknown>, step: number) {
  const pad = ' '.repeat(step)
  const lines = Object.entries(body).map(
    ([key, value]) => `${pad}"${key}": ${JSON.stringify(value)}`,
  )
  return `{\n${lines.join(',\n')}\n}`
}

function indent(text: string, spaces: number) {
  const pad = ' '.repeat(spaces)
  return text
    .split('\n')
    .map((line, index) => (index === 0 ? line : pad + line))
    .join('\n')
}

/** 生成可直接复制运行的调用示例（不依赖官方 SDK）。 */
export function buildCallExample(language: ExampleLanguage, input: ExampleInput) {
  const url = protocolEndpoint(input.protocol, input.modelID, input.baseURL)
  const headers = Object.entries(protocolHeaders(input.protocol, input.apiKey))
  const body = protocolBody(input.protocol, input.modelID)

  if (language === 'curl') {
    const headerLines = headers.map(([name, value]) => `  -H "${name}: ${value}" \\`)
    return [
      `curl ${url} \\`,
      ...headerLines,
      `  -d '${indent(formatBody(body, 2), 2)}'`,
    ].join('\n')
  }

  if (language === 'python') {
    const headerLines = headers.map(([name, value]) => `        "${name}": "${value}",`)
    return [
      'import requests',
      '',
      'response = requests.post(',
      `    "${url}",`,
      '    headers={',
      ...headerLines,
      '    },',
      `    json=${indent(formatBody(body, 4), 4)},`,
      ')',
      'print(response.json())',
    ].join('\n')
  }

  const headerLines = headers.map(([name, value]) => `    "${name}": "${value}",`)
  return [
    `const response = await fetch("${url}", {`,
    '  method: "POST",',
    '  headers: {',
    ...headerLines,
    '  },',
    `  body: JSON.stringify(${indent(formatBody(body, 2), 2)}),`,
    '})',
    'console.log(await response.json())',
  ].join('\n')
}
