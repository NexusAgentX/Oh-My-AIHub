import { Checkbox } from '../ui'

export type Compatibility = { responses?: string[]; codex?: string[] }
export const protocolLabels = { responses: 'Responses', codex: 'Codex' }
const featureLabels: Record<string, string> = {
  inference: '推理接口', nonstream: '非流式', stream: '流式', background: '后台响应', response_read: '响应读取与续接', response_delete: '删除响应', response_cancel: '取消响应', input_items: '读取输入条目', conversations: '会话资源', input_tokens: 'Token 计数', compact: '压缩接口', zstd: 'zstd 请求',
  search: '独立搜索', images_generate: '独立生图', images_edit: '独立图片编辑',
  function_tools: '函数工具', custom_tools: 'Custom 工具', tool_namespaces: '工具命名空间', tool_search: '工具搜索', parallel_tools: '并行工具调用', structured_output: '结构化输出', reasoning: '推理参数', encrypted_reasoning: '加密推理', reasoning_all_turns: '跨轮推理', vision: '图片输入',
  web_search: '内置搜索', image_generation: '内置生图', responses_lite: 'Responses Lite', compact_v1: 'V1 压缩', compact_v2: 'V2 压缩', compaction_input: '压缩条目输入', fast: 'Fast / Priority', ultrafast: 'Ultrafast',
  file_search: '文件搜索', code_interpreter: '代码解释器', mcp: '托管 MCP', computer: '计算机工具', shell: 'Shell 工具', apply_patch: 'Apply Patch',
}
export const capabilityLabels: Record<string, string> = { ...featureLabels, codex_backend: 'Codex HTTP', codex_search: '独立搜索', priority: 'Priority' }
const commonChannel = ['inference', 'nonstream', 'stream', 'background', 'response_read', 'response_delete', 'response_cancel', 'input_items', 'conversations', 'input_tokens', 'compact', 'zstd']
const commonModel = ['function_tools', 'custom_tools', 'tool_namespaces', 'tool_search', 'parallel_tools', 'structured_output', 'reasoning', 'encrypted_reasoning', 'reasoning_all_turns', 'vision', 'web_search', 'image_generation', 'responses_lite', 'compact_v1', 'compact_v2', 'compaction_input', 'file_search', 'code_interpreter', 'mcp', 'computer', 'shell', 'apply_patch', 'fast', 'ultrafast']
export const channelCapabilities = [...commonChannel.map(v => `responses/${v}`), ...[...commonChannel, 'search', 'images_generate', 'images_edit'].map(v => `codex/${v}`)]
export const modelCapabilities = ['responses', 'codex'].flatMap(protocol => commonModel.map(v => `${protocol}/${v}`))
export function matrixCells(matrix?: Compatibility): string[] {
  return Object.entries(matrix ?? {}).flatMap(([protocol, features]) => features.map(feature => `${protocol}/${feature}`))
}
export function cellsMatrix(cells: string[] = []): Compatibility {
  return { responses: cells.filter(v => v.startsWith('responses/')).map(v => v.slice(10)), codex: cells.filter(v => v.startsWith('codex/')).map(v => v.slice(6)) }
}
export function capabilityLabel(value: string) {
  const [protocol, feature] = value.split('/')
  return feature ? `${protocolLabels[protocol as keyof typeof protocolLabels] ?? protocol} · ${featureLabels[feature] ?? feature}` : capabilityLabels[value] ?? value
}
export function CapabilityToggles({ values = [], options, onChange, passed = {}, errors = {} }: { values?: string[]; options: string[]; onChange: (values: string[]) => void; passed?: Record<string, boolean>; errors?: Record<string, string> }) {
  const features = [...new Set(options.map(v => v.split('/')[1]))]
  return <div className="compatibility-matrix"><table><caption className="sr-only">兼容性矩阵</caption><thead><tr><th scope="col">能力</th><th scope="col">Responses</th><th scope="col">Codex</th></tr></thead><tbody>{features.map(feature => <tr key={feature}><th scope="row">{featureLabels[feature] ?? feature}</th>{(['responses', 'codex'] as const).map(protocol => {
    const cell = `${protocol}/${feature}`
    return <td key={protocol}>{options.includes(cell) ? <span title={errors[cell] ?? (passed[cell] === undefined ? '未验证，可手动声明' : passed[cell] ? '探测通过' : '探测未通过')}><Checkbox aria-label={`${protocolLabels[protocol]} · ${featureLabels[feature] ?? feature}`} label={`${protocolLabels[protocol]} · ${featureLabels[feature] ?? feature}`} checked={values.includes(cell)} onChange={e => onChange(e.target.checked ? [...new Set([...values, cell])] : values.filter(v => v !== cell))} /><span aria-label={passed[cell] === undefined ? '未验证' : passed[cell] ? '探测通过' : '探测未通过'} className={passed[cell] === undefined ? 'muted' : passed[cell] ? 'compatibility-pass' : 'compatibility-fail'}>{passed[cell] === undefined ? '—' : passed[cell] ? '✓' : '×'}</span></span> : '—'}</td>
  })}</tr>)}</tbody></table></div>
}
export function CapabilityTags({ values = [] }: { values?: string[] }) {
  return <span className="format-toggles">{values.map(value => <span className="badge badge-neutral" key={value}>{capabilityLabel(value)}</span>)}</span>
}
