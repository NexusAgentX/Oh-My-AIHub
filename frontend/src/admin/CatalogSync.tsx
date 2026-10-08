import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { request } from '../api/client'
import { errorMessage } from '../api/query'
import type { components } from '../api/schema.gen'
import { Button, Card, Checkbox, InlineError, TextField } from '../ui'
import { Collapsible } from './components'
import type { AdminModel } from './types'

type Status = components['schemas']['CatalogSyncStatus']
export function CatalogSync() {
  const client = useQueryClient()
  const status = useQuery({ queryKey: ['admin', 'catalog-sync'], queryFn: () => request<Status>('/api/admin/catalog-sync'), refetchInterval: 3000 })
  useEffect(() => { if (status.data?.finished_at) void client.invalidateQueries({ queryKey: ['admin', 'models'] }) }, [status.data?.finished_at, client])
  const [rate, setRate] = useState<string | null>(null)
  const save = useMutation({ mutationFn: () => request<Status>('/api/admin/catalog-sync', { method: 'PUT', body: JSON.stringify({ exchange_rate: rate ?? status.data?.exchange_rate ?? '' }) }), onSuccess: () => { setRate(null); void client.invalidateQueries({ queryKey: ['admin'] }) } })
  const run = useMutation({ mutationFn: () => request('/api/admin/catalog-sync/run', { method: 'POST' }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['admin'] }) } })
  return <Card><Collapsible title="Bifrost 自动同步" badge={status.data?.status === 'running' ? '同步中' : undefined}>
    <div className="stack-form">
      <TextField label="美元换算率（积分 / USD）" hint="未设置时仅同步资料，新模型保持停用。保存后在下次同步应用。" value={rate ?? status.data?.exchange_rate ?? ''} onChange={e => setRate(e.target.value)} inputMode="decimal" />
      <div className="form-actions"><Button type="button" loading={save.isPending} onClick={() => save.mutate()}>保存换算率</Button><Button type="button" variant="secondary" disabled={status.data?.status === 'running'} loading={run.isPending} onClick={() => run.mutate()}>立即同步</Button></div>
      <p className="muted-copy">启动及每 24 小时更新聊天 / Responses 模型。新增模型默认停用，已有手工模型不接管。</p>
      {status.data && <p>最近状态：{({ idle: '尚未运行', running: '同步中', succeeded: '完成', failed: '失败' } as Record<string, string>)[status.data.status] ?? status.data.status} · {status.data.finished_at ? new Date(status.data.finished_at).toLocaleString() : '—'}<br />新增 {status.data.result.created ?? 0} · 更新 {status.data.result.updated ?? 0} · 需人工处理 {status.data.result.needs_review ?? 0} · 冲突 {status.data.result.conflict ?? 0}</p>}
      <InlineError>{status.data?.error || (status.isError ? errorMessage(status.error, '请求失败') : '') || (save.isError ? errorMessage(save.error, '请求失败') : '') || (run.isError ? errorMessage(run.error, '请求失败') : '')}</InlineError>
    </div>
  </Collapsible></Card>
}
export function SourceDetails({ model, syncing, onChange }: { model: AdminModel; syncing: boolean; onChange: (v: boolean) => void }) {
  const source = model.source
  const [showRaw, setShowRaw] = useState(false)
  const raw = useQuery({ queryKey: ['admin', 'source', model.id], queryFn: () => request<{ record: object }>(`/api/admin/models/${encodeURIComponent(model.id)}/source`), enabled: showRaw })
  if (!source) return null
  const meta = source.metadata
  const value = (key: string) => meta[key] == null ? '未提供' : typeof meta[key] === 'boolean' ? (meta[key] ? '支持' : '不支持') : String(meta[key])
  return <div className="stack-form">
    <Checkbox label="从 Bifrost 同步此模型" checked={syncing} onChange={e => onChange(e.target.checked)} />
    <small className="muted-copy">同步覆盖名称、价格和能力；启停、排序和备注由本地管理。关闭后可手动修改，恢复后下次同步覆盖。</small>
    <Collapsible title="来源资料" badge={source.status === 'waiting_rate' ? '待设置汇率' : source.status === 'needs_review' ? '需人工处理' : source.status === 'missing' ? '源中已缺失' : undefined}>
      <div className="stack-form">
        <p className="mono" style={{ overflowWrap: 'anywhere' }}>来源键：{source.key}</p>
        <p style={{ overflowWrap: 'anywhere' }}>资料来源：{value('source')}</p>
        <p>提供方：{value('provider')} · 原模型：{value('base_model')}<br />最大输入：{value('max_input_tokens')} · 最大输出：{value('max_output_tokens')}<br />工具调用：{value('supports_function_calling')} · 结构化输出：{value('supports_response_schema')} · 视觉：{value('supports_vision')}<br />弃用：{meta.is_deprecated == null ? '未提供' : meta.is_deprecated ? '是' : '否'} · 日期：{value('deprecation_date')}</p>
        <p>有效价格：{source.price_ready ? `已配置，最近同步汇率 ${source.applied_rate || '手工'}` : '尚未配置，不能启用'}<br />最近应用：{source.last_applied_at ? new Date(source.last_applied_at).toLocaleString() : '—'}<br />最近发现：{new Date(source.last_seen_at).toLocaleString()}</p>
        {source.problems.length > 0 && <InlineError>{source.problems.join('；')}</InlineError>}
        {source.warnings.length > 0 && <p className="muted-copy">{source.warnings.join('；')}</p>}
        <small>来源键与本地调用名可能不同；渠道上游模型名需按实际接口配置。</small>
        <Button type="button" variant="secondary" onClick={() => setShowRaw(!showRaw)}>{showRaw ? '收起原始资料' : '查看原始资料'}</Button>
        {showRaw && <pre style={{ maxHeight: 300, overflow: 'auto', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{raw.data ? JSON.stringify(raw.data.record, null, 2) : raw.isError ? errorMessage(raw.error, '请求失败') : '加载中…'}</pre>}
      </div>
    </Collapsible>
  </div>
}
