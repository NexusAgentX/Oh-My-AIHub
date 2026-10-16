export const capabilityLabels: Record<string, string> = {
  codex_backend: 'Codex HTTP', codex_search: '独立搜索', web_search: '内置搜索', image_generation: '内置生图',
  responses_lite: 'Responses Lite', compact_v1: 'V1 压缩', compact_v2: 'V2 压缩',
  priority: 'Priority', ultrafast: 'Ultrafast',
}
export const channelCapabilities = ['codex_backend', 'codex_search']
export const modelCapabilities = ['web_search', 'image_generation', 'responses_lite', 'compact_v1', 'compact_v2', 'priority', 'ultrafast']
export function CapabilityToggles({ values = [], options, onChange }: {values?: string[]; options: string[]; onChange: (values: string[]) => void}) {
  return <span className="format-toggles" role="group" aria-label="声明支持的能力">{options.map(value => <button type="button" key={value} className={`format-toggle ${values.includes(value) ? 'format-toggle-on' : ''}`} aria-pressed={values.includes(value)} onClick={() => onChange(values.includes(value) ? values.filter(v => v !== value) : [...values, value])}>{capabilityLabels[value]}</button>)}</span>
}
export function CapabilityTags({ values = [] }: {values?: string[]}) {
  return <span className="format-toggles">{values.map(value => <span className="badge badge-neutral" key={value}>{capabilityLabels[value] ?? value}</span>)}</span>
}
