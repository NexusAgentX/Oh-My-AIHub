import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { FormEvent } from 'react'
import type { components } from '../api/schema.gen'
import type { RequestBody } from '../api/types'
import { apiSend } from '../api/client'
import { errorMessage } from '../api/query'
import { SupplierIcon } from '../sharing/components'
import { useAdminSuppliers, useSaveSupplier } from '../sharing/queries'
import { Badge, Button, Card, Checkbox, EmptyState, InlineError, PageHeader, QueryBoundary, SearchInput, SelectField, TextareaField, TextField, Toolbar } from '../ui'

type AdminSupplier = components['schemas']['AdminSupplier']
const empty = (): RequestBody<'createSupplier'> => ({ name: '', kind: 'relay', summary: '', description: '', website_url: '', docs_url: '', status_url: '', icon_data: '', icon_source: '', visible: true, endpoints: [] })
function SupplierImpact({ endpoints }: { endpoints: RequestBody<'createSupplier'>['endpoints'] }) {
  const [addresses, setAddresses] = useState(endpoints)
  useEffect(() => { const timer = setTimeout(() => setAddresses(endpoints), 400); return () => clearTimeout(timer) }, [endpoints])
  const query = useQuery({ queryKey: ['sharing', 'impact', addresses], retry: false,
    queryFn: () => apiSend<'previewSupplierImpact'>('POST', '/api/admin/suppliers/preview', { endpoints: addresses }) })
  return <p className="muted-copy" role="status">{addresses !== endpoints || query.isPending ? '正在计算关联渠道…' : query.isError ? '填写有效地址后可预览关联渠道数' : `保存后关联 ${query.data.linked_channels} 个已上架渠道`}</p>
}
function SupplierEditor({ supplier, onClose }: { supplier: AdminSupplier | null; onClose: () => void }) {
  const [form, setForm] = useState<RequestBody<'createSupplier'>>(() => supplier ? {
    name: supplier.name, kind: supplier.kind, summary: supplier.summary, description: supplier.description, website_url: supplier.website_url,
    docs_url: supplier.docs_url, status_url: supplier.status_url, icon_data: supplier.icon_data, icon_source: supplier.icon_source, visible: supplier.visible, endpoints: supplier.endpoints,
  } : empty())
  const [error, setError] = useState('')
  const [iconError, setIconError] = useState('')
  const [fetching, setFetching] = useState(false)
  const upload = useRef<HTMLInputElement>(null)
  const save = useSaveSupplier()
  const set = <K extends keyof typeof form>(key: K, value: typeof form[K]) => setForm((old) => ({ ...old, [key]: value }))
  const fetchIcon = async () => {
    if (!form.website_url || fetching) return
    const source = form.website_url
    setFetching(true); setIconError('')
    try { const result = await apiSend<'fetchSupplierIcon'>('POST', '/api/admin/suppliers/icon', { website_url: source }); setForm((current) => current.website_url === source ? { ...current, ...result } : current) }
    catch (e) { setIconError(errorMessage(e, '图标获取失败，可直接保存')) }
    finally { setFetching(false) }
  }
  const submit = (event: FormEvent) => { event.preventDefault(); setError(''); save.mutate({ id: supplier?.id, body: form }, { onSuccess: onClose, onError: (e) => setError(errorMessage(e, '保存失败')) }) }
  return <Card title={supplier ? '编辑供应商' : '新增供应商'}><form className="supplier-form" onSubmit={submit}>
    <TextField label="名称" required maxLength={64} value={form.name} onChange={(e) => set('name', e.target.value)} />
    <SelectField label="类型" value={form.kind} onChange={(e) => set('kind', e.target.value as 'official' | 'relay')}><option value="official">官方 API</option><option value="relay">中转站</option></SelectField>
    <TextField label="官网" type="url" required placeholder="https://" value={form.website_url} onChange={(e) => set('website_url', e.target.value)} onBlur={() => { if (!form.icon_data) void fetchIcon() }} />
    <div className="supplier-preview"><SupplierIcon supplier={form} /><Button type="button" variant="secondary" loading={fetching} onClick={fetchIcon}>获取图标</Button><Button type="button" variant="quiet" disabled={fetching} onClick={() => { set('icon_data', ''); set('icon_source', '') }}>移除图标</Button></div>
    <div><Button type="button" variant="secondary" disabled={fetching} onClick={() => upload.current?.click()}>上传图标</Button><p className="muted-copy">PNG / JPEG / GIF，最大 256 KB、1024 × 1024</p><input ref={upload} aria-label="上传图标" hidden type="file" disabled={fetching} accept="image/png,image/jpeg,image/gif" onChange={(e) => {
      const file = e.target.files?.[0]; if (!file) return
      if (file.size > 256 * 1024 || !['image/png', 'image/jpeg', 'image/gif'].includes(file.type)) { setIconError('请选择 256 KB 以内的 PNG、JPEG 或 GIF'); return }
      const reader = new FileReader(); reader.onload = () => { set('icon_data', String(reader.result)); set('icon_source', ''); setIconError('') }; reader.readAsDataURL(file)
    }} /></div>
    {form.icon_source && <a href={form.icon_source} target="_blank" rel="noopener noreferrer">图标来源 ↗</a>}<InlineError>{iconError}</InlineError>
    <TextField label="一句话简介" maxLength={160} value={form.summary} onChange={(e) => set('summary', e.target.value)} />
    <TextareaField label="详细介绍（Markdown）" rows={5} maxLength={8000} value={form.description} onChange={(e) => set('description', e.target.value)} />
    <TextField label="文档地址" type="url" value={form.docs_url} onChange={(e) => set('docs_url', e.target.value)} />
    <TextField label="状态页地址" type="url" value={form.status_url} onChange={(e) => set('status_url', e.target.value)} />
    <div><strong>API 地址</strong><SupplierImpact endpoints={form.endpoints} /></div>
    {form.endpoints.map((endpoint, index) => <div className="supplier-endpoint" key={index}><TextField label={`Base URL ${index + 1}`} type="url" required value={endpoint.base_url} onChange={(e) => set('endpoints', form.endpoints.map((value, i) => i === index ? { ...value, base_url: e.target.value } : value))} /><TextField label="标签" maxLength={64} value={endpoint.label} onChange={(e) => set('endpoints', form.endpoints.map((value, i) => i === index ? { ...value, label: e.target.value } : value))} /><Button type="button" variant="quiet" onClick={() => set('endpoints', form.endpoints.filter((_, i) => i !== index))}>移除</Button></div>)}
    <Button type="button" variant="secondary" disabled={form.endpoints.length >= 32} onClick={() => set('endpoints', [...form.endpoints, { base_url: '', label: '' }])}>添加 API 地址</Button>
    <Checkbox label="在供应商目录展示" checked={form.visible} onChange={(e) => set('visible', e.target.checked)} />
    <InlineError>{error}</InlineError><div className="sharing-line"><Button type="submit" loading={save.isPending} disabled={fetching}>保存</Button><Button type="button" variant="secondary" onClick={onClose}>取消</Button></div>
  </form></Card>
}
export function SuppliersPage() {
  const query = useAdminSuppliers()
  const [editing, setEditing] = useState<AdminSupplier | null | undefined>()
  const [search, setSearch] = useState('')
  return <><PageHeader title="供应商" actions={editing === undefined ? <Button onClick={() => setEditing(null)}>新增供应商</Button> : undefined} />
    {editing !== undefined ? <SupplierEditor key={editing?.id ?? 'new'} supplier={editing} onClose={() => setEditing(undefined)} /> : <><Toolbar><SearchInput label="搜索供应商" value={search} onChange={(e) => setSearch(e.target.value)} /></Toolbar><QueryBoundary query={query} errorFallback="供应商加载失败">{(items) => { const matches = items.filter((value) => value.name.toLowerCase().includes(search.toLowerCase())); return matches.length ? <div className="sharing-grid">{matches.map((supplier) => <Card key={supplier.id}><div className="sharing-title"><SupplierIcon supplier={supplier} /><strong>{supplier.name}</strong><Badge>{supplier.visible ? '展示中' : '已隐藏'}</Badge></div><p>{supplier.endpoints.length} 个 API 地址 · {supplier.linked_channels} 个关联渠道</p><Button variant="secondary" onClick={() => setEditing(supplier)}>编辑</Button></Card>)}</div> : <EmptyState title="暂无符合条件的供应商" /> }}</QueryBoundary></>}
  </>
}
