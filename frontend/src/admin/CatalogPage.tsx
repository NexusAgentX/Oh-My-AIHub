import { errorMessage } from '../api/query'
import { useState, type FormEvent } from 'react'
import { useInfiniteQuery, keepPreviousData } from '@tanstack/react-query'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { apiGet } from '../api/client'
import { Badge, Button, ButtonLink, Card, DataTable, PageHeader, QueryBoundary, SelectField, TextField, Toolbar } from '../ui'
import { Pager } from '../ui/Pager'
import { CommittedInput } from '../ui/CommittedInput'
import { enumField, stringField, useUrlState } from '../ui/urlState'

const schema = { kind: enumField(['supplier', 'group', 'group_model'] as const, 'supplier'), parent_id: stringField('') }
const names = { supplier: '供应商', group: '分组', group_model: '模型报价' }
export function CatalogPage() {
  const [filters, setFilters] = useUrlState(schema), location = useLocation(), navigate = useNavigate(), [modelID, setModelID] = useState('')
  const query = useInfiniteQuery({ queryKey: ['contributions', 'catalog', filters], initialPageParam: '', queryFn: ({ pageParam }) => apiGet<'contributionTargets'>('/api/contributions/targets', { ...filters, cursor: pageParam || undefined, limit: 50 }), getNextPageParam: (last, pages) => pages.length < 10 ? last.next_cursor ?? undefined : undefined, placeholderData: keepPreviousData })
  const limited = query.data?.pages.length === 10 && Boolean(query.data.pages.at(-1)?.next_cursor)
  const from = { to: '/admin/catalog' + location.search, label: '目录治理' }
  const revise = (kind: string, action: string, target?: string) => {
    const params = new URLSearchParams(location.search)
    params.set('create_kind', kind); params.set('action', action)
    if (target) params.set('target_id', target); else params.delete('target_id')
    return `/admin/catalog/new?${params}`
  }
  const state = { overlayFrom: from.to, from }
  const govern = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); const action = (event.nativeEvent as SubmitEvent).submitter?.getAttribute('value') === 'restore' ? 'restore' : 'disable'; if (modelID.trim()) navigate(revise('model_id', action, modelID.trim()), { state }) }
  return <><PageHeader title="目录治理" actions={<ButtonLink to={revise(filters.kind, 'new', filters.parent_id || undefined)} state={state}>新增{names[filters.kind]}</ButtonLink>} />
    <Toolbar><SelectField label="对象" value={filters.kind} onChange={event => setFilters({ kind: event.target.value as typeof filters.kind, parent_id: '' })}>{Object.entries(names).map(([kind, name]) => <option key={kind} value={kind}>{name}</option>)}</SelectField>{filters.kind !== 'supplier' && <label className="field"><span className="field-label">{filters.kind === 'group' ? '供应商' : '分组'} ID</span><CommittedInput className="input" value={filters.parent_id} onCommit={parent_id => setFilters({ parent_id: parent_id.trim() })} /></label>}{filters.parent_id && <Button variant="quiet" onClick={() => setFilters({ parent_id: '' })}>清除上级筛选</Button>}</Toolbar>
    <Card flush><QueryBoundary query={query}>{data => <div aria-busy={query.isFetching}><DataTable caption={`${names[filters.kind]}治理`} rowKey={row => row.id} rows={data.pages.flatMap(page => page.items)} columns={[
      { key: 'name', header: names[filters.kind], primary: true, cell: row => <><strong>{row.name}</strong><small className="mono">{row.id}</small></> },
      { key: 'enabled', header: '状态', cell: row => <Badge tone={row.enabled ? 'success' : 'neutral'}>{row.enabled ? '已启用' : '未启用'}</Badge> },
      { key: 'actions', header: '发起修订', cell: row => <div className="table-actions"><ButtonLink size="sm" variant="secondary" to={revise(filters.kind, 'edit', row.id)} state={state}>修改</ButtonLink><ButtonLink size="sm" variant="quiet" to={revise(filters.kind, row.enabled ? 'disable' : 'restore', row.id)} state={state}>{row.enabled ? '停用' : '恢复'}</ButtonLink><ButtonLink size="sm" variant="quiet" to={revise(filters.kind, 'delete', row.id)} state={state}>删除</ButtonLink>{filters.kind !== 'group_model' && <ButtonLink size="sm" variant="quiet" to={`/admin/catalog?kind=${filters.kind === 'supplier' ? 'group' : 'group_model'}&parent_id=${row.id}`}>{filters.kind === 'supplier' ? '查看分组' : '查看报价'}</ButtonLink>}</div> },
    ]} /><Pager hasMore={query.hasNextPage || limited} limited={limited} error={query.isFetchNextPageError ? errorMessage(query.error, '加载失败，请重试') : undefined} loading={query.isFetchingNextPage} onMore={() => void query.fetchNextPage()} /></div>}</QueryBoundary></Card>
    <Card title="模型 ID 治理"><form className="stack-form" onSubmit={govern}><TextField label="模型 ID" value={modelID} onChange={event => setModelID(event.target.value)} required /><div className="form-actions"><Button type="submit" value="disable" variant="secondary" disabled={!modelID.trim()}>发起停用修订</Button><Button type="submit" value="restore" variant="quiet" disabled={!modelID.trim()}>发起恢复修订</Button></div></form></Card>
    <Outlet />
  </>
}
