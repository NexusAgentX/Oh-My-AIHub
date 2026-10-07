import { useNavigate, useSearchParams } from 'react-router-dom'
import type { APIKey } from '../api/types'
import {
  Button,
  Card,
  CountBadge,
  DataTable,
  EmptyState,
  Icon,
  PageHeader,
  QueryBoundary,
  type Column,
} from '../ui'
import { KeyEditorDrawer } from './KeyEditorDrawer'
import { GatewayStatusBadge, formatDate } from './presentation'
import { useAPIKeysQuery } from './queries'

const columns: Column<APIKey>[] = [
  {
    key: 'name',
    header: '名称 / 前缀',
    primary: true,
    cell: (key) => (
      <>
        <strong>{key.display_name}</strong>
        <small className="mono">{key.prefix}…</small>
      </>
    ),
  },
  { key: 'status', header: '状态', cell: (key) => <GatewayStatusBadge status={key.status} /> },
  { key: 'routes', header: '路由', numeric: true, cell: (key) => key.pools.length },
  { key: 'used', header: '最后调用', cell: (key) => formatDate(key.last_used_at) },
]

export function APIKeysPage() {
  const query = useAPIKeysQuery()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const creating = searchParams.get('new') === '1'

  const setCreating = (open: boolean) => {
    const next = new URLSearchParams(searchParams)
    if (open) next.set('new', '1')
    else next.delete('new')
    setSearchParams(next, { replace: true })
  }

  const createButton = (
    <Button icon={<Icon name="plus" />} onClick={() => setCreating(true)}>
      新建 Key
    </Button>
  )

  return (
    <>
      <PageHeader actions={createButton} title="API Keys" />
      <QueryBoundary errorFallback="API Key 加载失败" query={query}>
        {(keys) => {
          const visible = keys.filter((key) => key.status !== 'deleted')
          return (
            <Card actions={<CountBadge>{visible.length}</CountBadge>} flush title="访问凭据">
              <DataTable
                caption="API Keys"
                columns={columns}
                empty={
                  <EmptyState
                    action={createButton}
                    description="创建后即可为模型配置路由并调用。"
                    title="还没有 API Key"
                  />
                }
                rowHref={(key) => `/keys/${key.id}`}
                rowKey={(key) => key.id}
                rows={visible}
              />
            </Card>
          )
        }}
      </QueryBoundary>
      <KeyEditorDrawer
        onClose={() => setCreating(false)}
        onSaved={({ key, secret }) =>
          navigate(`/keys/${key.id}`, { state: { secret } })
        }
        open={creating}
      />
    </>
  )
}
