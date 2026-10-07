import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { Link, useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import type { APIKey, APIKeyPool, APIKeyPoolMember } from '../api/contracts'
import { ApiError } from '../api/client'
import { errorMessage } from '../api/query'
import {
  Badge,
  Button,
  Card,
  CountBadge,
  DataTable,
  Dialog,
  Drawer,
  EmptyState,
  Icon,
  InlineError,
  Notice,
  PageHeader,
  QueryBoundary,
  type Column,
} from '../ui'
import { CallExamples, SecretValue } from './CallExamples'
import { KeyEditorDrawer } from './KeyEditorDrawer'
import {
  GatewayStatusBadge,
  PricePair,
  formatDate,
  formatRate,
  protocolLabels,
} from './presentation'
import { gatewayKeys, useAPIKeyQuery, useKeyActionMutation, type KeyAction } from './queries'

type SecretState = { secret?: string } | null

const actionCopy: Record<
  KeyAction,
  { title: string; confirm: string; description?: string; danger?: boolean }
> = {
  rotate: {
    title: '轮换 API Key',
    confirm: '轮换并停用旧 Key',
    description: '旧 Key 会立即失效，新 Key 只显示一次。',
  },
  disable: { title: '停用 API Key', confirm: '确认停用' },
  enable: { title: '启用 API Key', confirm: '确认启用' },
  delete: {
    title: '删除 API Key',
    confirm: '确认删除',
    description: '删除后不可恢复，历史调用与账单仍会保留。',
    danger: true,
  },
}

const memberColumns: Column<APIKeyPoolMember>[] = [
  {
    key: 'channel',
    header: '备用顺序 / 渠道',
    primary: true,
    cell: (member) => (
      <>
        <strong>
          #{member.priority} {member.channel_name}
        </strong>
        <small>{member.provider_name}</small>
      </>
    ),
  },
  {
    key: 'eligible',
    header: '资格',
    cell: (member) => (
      <Badge tone={member.eligible ? 'success' : 'warning'}>
        {member.eligible ? '可用' : '需更新'}
      </Badge>
    ),
  },
  {
    key: 'price',
    header: '输入 / 输出',
    cell: (member) => (
      <PricePair first={member.input_price} second={member.output_price} tiers={member.price_tiers} />
    ),
  },
  {
    key: 'cache',
    header: '缓存写 / 读',
    hideOnMobile: true,
    cell: (member) => <PricePair first={member.cache_write_price} second={member.cache_read_price} />,
  },
  { key: 'rate', header: '成功率', numeric: true, cell: (member) => formatRate(member.success_rate) },
  {
    key: 'speed',
    header: 'TTFT / TPS',
    numeric: true,
    hideOnMobile: true,
    cell: (member) => `${member.ttft_milliseconds ?? '—'} ms / ${member.tokens_per_second ?? '—'}`,
  },
]

export function APIKeyPage() {
  const { keyID = '' } = useParams()
  const query = useAPIKeyQuery(keyID)
  return (
    <QueryBoundary errorFallback="API Key 加载失败" query={query}>
      {(key) => <KeyDetail key={key.id} keyData={key} refetch={() => void query.refetch()} />}
    </QueryBoundary>
  )
}

function KeyDetail({ keyData: key, refetch }: { keyData: APIKey; refetch: () => void }) {
  const location = useLocation()
  const navigate = useNavigate()
  const client = useQueryClient()
  const [searchParams, setSearchParams] = useSearchParams()
  const action = useKeyActionMutation()
  const [secret, setSecret] = useState((location.state as SecretState)?.secret ?? '')
  const [pending, setPending] = useState<KeyAction | null>(null)
  const [exampleRoute, setExampleRoute] = useState<APIKeyPool | null>(null)
  const [error, setError] = useState('')
  const editing = searchParams.get('settings') === '1'
  const editable = key.status !== 'deleted'

  useEffect(() => {
    // 完整 Key 只保留在组件内存中，立即从历史状态里清除
    if ((location.state as SecretState)?.secret) {
      navigate(`${location.pathname}${location.search}`, { replace: true, state: null })
    }
  }, [location.pathname, location.search, location.state, navigate])

  const setEditing = (open: boolean) => {
    const next = new URLSearchParams(searchParams)
    if (open) next.set('settings', '1')
    else next.delete('settings')
    setSearchParams(next, { replace: true })
  }

  const confirm = async () => {
    if (!pending) return
    setError('')
    try {
      const result = await action.mutateAsync({ keyID: key.id, version: key.version, action: pending })
      if (pending === 'delete') {
        setSecret('')
        client.removeQueries({ queryKey: gatewayKeys.key(key.id) })
        navigate('/keys', { replace: true })
        return
      }
      if (pending === 'rotate') setSecret(result.secret)
      setPending(null)
    } catch (caught) {
      setError(
        caught instanceof ApiError && caught.code === 'conflict'
          ? 'Key 状态已变化，已重新加载，请重试'
          : errorMessage(caught, '操作失败'),
      )
      if (caught instanceof ApiError && caught.code === 'conflict') refetch()
      setPending(null)
    }
  }

  const copy = pending ? actionCopy[pending] : null

  return (
    <>
      <PageHeader
        actions={
          editable && (
            <>
              <Button icon={<Icon name="settings" />} onClick={() => setEditing(true)} variant="secondary">
                设置
              </Button>
              <Button onClick={() => setPending('rotate')} variant="secondary">
                轮换
              </Button>
              {key.status === 'active' && (
                <Button onClick={() => setPending('disable')} variant="secondary">
                  停用
                </Button>
              )}
              {key.status === 'disabled' && (
                <Button onClick={() => setPending('enable')} variant="secondary">
                  启用
                </Button>
              )}
              <Button onClick={() => setPending('delete')} variant="danger">
                删除
              </Button>
            </>
          )
        }
        back={
          <Link className="back-link" to="/keys">
            ← API Keys
          </Link>
        }
        description={
          <span className="key-meta">
            <span className="mono">{key.prefix}…</span>
            <GatewayStatusBadge status={key.status} />
            <span>第 {key.generation} 代</span>
            <span>最后调用 {formatDate(key.last_used_at)}</span>
          </span>
        }
        title={key.display_name}
      />
      <InlineError>{error}</InlineError>

      {secret && (
        <Card className="one-time-key" title="保存新 Key">
          <div className="one-time-key-body">
            <Notice tone="warning">完整 Key 只显示这一次。</Notice>
            <SecretValue secret={secret} />
            {key.pools.length > 0 && (
              <CallExamples
                apiKey={secret}
                modelID={key.pools[0].model_id}
                protocol={key.pools[0].protocol}
              />
            )}
            <div>
              <Button onClick={() => setSecret('')} variant="quiet">
                我已保存
              </Button>
            </div>
          </div>
        </Card>
      )}

      <h2 className="section-heading">
        路由 <CountBadge>{key.pools.length}</CountBadge>
      </h2>
      {key.pools.length === 0 ? (
        <Card flush>
          <EmptyState
            action={
              editable && (
                <Button onClick={() => setEditing(true)} variant="secondary">
                  设置路由
                </Button>
              )
            }
            title="没有已配置的路由"
          />
        </Card>
      ) : (
        <div className="route-sections">
          {key.pools.map((pool) => (
            <Card
              actions={
                <Button onClick={() => setExampleRoute(pool)} size="sm" variant="secondary">
                  调用示例
                </Button>
              }
              flush
              key={pool.id}
              title={
                <span className="route-title">
                  {pool.model_name}
                  <small>{protocolLabels[pool.protocol]}</small>
                </span>
              }
            >
              <DataTable
                caption={`${pool.model_name} 的备用顺序`}
                columns={memberColumns}
                rowKey={(member) => member.offer_id}
                rows={pool.members}
              />
            </Card>
          ))}
        </div>
      )}

      <KeyEditorDrawer
        keyData={key}
        onClose={() => setEditing(false)}
        onSaved={() => setEditing(false)}
        open={editing}
      />

      <Drawer
        description={exampleRoute ? protocolLabels[exampleRoute.protocol] : undefined}
        onClose={() => setExampleRoute(null)}
        open={Boolean(exampleRoute)}
        title="调用示例"
      >
        {exampleRoute && (
          <div className="examples-drawer">
            <Notice tone="info">示例中的 YOUR_API_KEY 请替换为完整 Key；需要新 Key 时轮换。</Notice>
            <CallExamples apiKey="" modelID={exampleRoute.model_id} protocol={exampleRoute.protocol} />
          </div>
        )}
      </Drawer>

      <Dialog
        busy={action.isPending}
        description={copy?.description}
        footer={
          <>
            <Button disabled={action.isPending} onClick={() => setPending(null)} variant="secondary">
              取消
            </Button>
            <Button
              loading={action.isPending}
              onClick={() => void confirm()}
              variant={copy?.danger ? 'danger' : 'primary'}
            >
              {copy?.confirm ?? ''}
            </Button>
          </>
        }
        onClose={() => setPending(null)}
        open={Boolean(pending)}
        title={copy?.title ?? ''}
      >
        {null}
      </Dialog>
    </>
  )
}
