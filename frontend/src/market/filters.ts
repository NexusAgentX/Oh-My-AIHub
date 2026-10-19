import { useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'
import { enumField, stringField, useUrlState } from '../ui/urlState'

export const protocols: Record<string, string> = { 'openai-chat-completions': 'OpenAI Chat', 'openai-responses': 'OpenAI Responses', 'native-messages': 'Anthropic Messages', 'native-generate-content': 'Gemini', 'openai-images': '图片', 'codex-special': 'Codex 专项兼容' }
export const featureLabels: Record<string, string> = { 'input:text': '文本输入', 'input:image': '图片输入', 'input:audio': '音频输入', 'input:video': '视频输入', 'input:document': '文档输入', 'output:text': '文本输出', 'output:image': '图片输出', 'output:audio': '音频输出', tools: '函数工具', strict_json: '严格结构化', json_object: 'JSON 输出', vision_url: '图片 URL', vision_inline: '请求内图片', reasoning: '推理配置', self_contained_replay: '完整推理状态续用', codex_cli_basic: 'Codex CLI 基本调用' }
export const marketSchema = {
  q: stringField(''), model_id: stringField(''), supplier_id: stringField(''), group_id: stringField(''),
  model_type: enumField(['', 'text', 'image'] as const, ''), format: stringField(''), protocol: stringField(''),
  sort: enumField(['id', 'price'] as const, 'id'), features: stringField(''), min_context: stringField(''), min_output: stringField(''),
  only_online: { defaultValue: false, parse: (value: string) => value === 'true' },
}
const hallSchema = { ...marketSchema, channel_q: stringField('') }
export const marketDefaults = Object.fromEntries(Object.entries(marketSchema).map(([key, field]) => [key, field.defaultValue])) as { [K in keyof typeof marketSchema]: typeof marketSchema[K]['defaultValue'] }

/** Snapshot cursors are deliberately discarded when opening an old bookmark. */
function useFirstPageURL() {
  const [params, setParams] = useSearchParams(), pagination = params.has('cursor') || params.has('limit')
  useEffect(() => { if (pagination) setParams(current => { const next = new URLSearchParams(current); next.delete('cursor'); next.delete('limit'); return next }, { replace: true }) }, [pagination, setParams])
}
export function useMarketFilters() { useFirstPageURL(); return useUrlState(marketSchema) }
export function useHallFilters() { useFirstPageURL(); return useUrlState(hallSchema) }
export function marketParams(value: Record<string, string | boolean>, hall = false) {
  const params = new URLSearchParams(), schema = hall ? hallSchema : marketSchema
  for (const [key, field] of Object.entries(schema)) { const item = value[key]; if (item !== undefined && item !== '' && item !== field.defaultValue) params.set(key, String(item)) }
  params.sort()
  return params
}

export function normalizeMarketParams(input: URLSearchParams, hall = false) {
  const params = new URLSearchParams()
  for (const [key, field] of Object.entries(hall ? hallSchema : marketSchema)) {
    const raw = input.get(key)
    const item = raw === null ? field.defaultValue : field.parse(raw)
    if (item !== '' && item !== field.defaultValue) params.set(key, String(item))
  }
  params.sort()
  return params
}
