import type { CatalogModel, Format } from '../api/types'

export type SampleTab = Format | 'cherry'

/** 每种格式选一个有在线渠道的模型；没有可用渠道的格式不出现。 */
export function modelsByFormat(models: CatalogModel[]) {
  const result: Partial<Record<Format, string>> = {}
  for (const model of models) {
    if (model.online_channels <= 0) continue
    for (const format of model.formats) result[format] ??= model.id
  }
  return result
}

const keyPlaceholder = 'YOUR_API_KEY'

/** 生成某种格式的 curl 示例，带入真实接口地址、Key 与模型。 */
export function curlSample(format: Format, origin: string, key: string | null, model: string) {
  const apiKey = key ?? keyPlaceholder
  switch (format) {
    case 'openai_chat':
      return `curl ${origin}/v1/chat/completions \\
  -H "Authorization: Bearer ${apiKey}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${model}",
    "messages": [{"role": "user", "content": "你好"}]
  }'`
    case 'openai_responses':
      return `curl ${origin}/v1/responses \\
  -H "Authorization: Bearer ${apiKey}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${model}",
    "input": "你好"
  }'`
    case 'anthropic':
      return `curl ${origin}/v1/messages \\
  -H "x-api-key: ${apiKey}" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${model}",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "你好"}]
  }'`
    case 'gemini':
      return `curl ${origin}/v1beta/models/${model}:generateContent \\
  -H "x-goog-api-key: ${apiKey}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "contents": [{"parts": [{"text": "你好"}]}]
  }'`
  }
}

/** Cherry Studio 填写项（OpenAI 兼容提供商）。 */
export function cherryStudioFields(origin: string, key: string | null, model: string | undefined) {
  return [
    { label: '提供商类型', value: 'OpenAI' },
    { label: 'API 地址', value: origin },
    { label: 'API 密钥', value: key ?? keyPlaceholder },
    { label: '模型 ID', value: model ?? '—' },
  ]
}
