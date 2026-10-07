import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import type { AdminChannelOffer } from '../api/contracts'
import { errorMessage } from '../api/query'
import {
  Button,
  Card,
  Checkbox,
  DataTable,
  Dialog,
  Drawer,
  EmptyState,
  InlineError,
  PageHeader,
  QueryBoundary,
  SuccessMessage,
  TextareaField,
} from '../ui'
import { AdminChannelBadge } from './AdminChannelBadge'
import {
  useAdminChannelQuery,
  useGovernChannel,
  useValidateOffer,
  useValidationAttemptsQuery,
} from './adminQueries'
import { formatDate, protocolLabels, ratingText } from './presentation'

type Action = { kind: 'validate'; offer: AdminChannelOffer } | { kind: 'pause' | 'delete' }

const actionTitles = { validate: '管理员重验', pause: '暂停渠道', delete: '删除渠道' } as const

export function AdminChannelPage() {
  const { channelID = '' } = useParams()
  const navigate = useNavigate()
  const query = useAdminChannelQuery(channelID)
  const govern = useGovernChannel()
  const validate = useValidateOffer()
  const [action, setAction] = useState<Action | null>(null)
  const [reason, setReason] = useState('')
  const [costConfirmed, setCostConfirmed] = useState(false)
  const [historyOffer, setHistoryOffer] = useState<AdminChannelOffer | null>(null)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const busy = govern.isPending || validate.isPending

  const close = () => {
    if (busy) return
    setAction(null)
    setReason('')
    setCostConfirmed(false)
  }

  const confirm = async (version: number) => {
    if (!action) return
    setError('')
    setMessage('')
    try {
      if (action.kind === 'validate') {
        const result = await validate.mutateAsync(action.offer.id)
        setMessage(result.status === 'passed' ? '验证通过' : '验证失败')
        setHistoryOffer(action.offer)
      } else {
        await govern.mutateAsync({
          channelID,
          action: action.kind,
          expectedVersion: version,
          reason: reason.trim(),
        })
        if (action.kind === 'delete') {
          navigate('/admin/channels', { replace: true })
          return
        }
      }
      setAction(null)
      setReason('')
      setCostConfirmed(false)
    } catch (caught) {
      setError(errorMessage(caught, '治理操作失败'))
    }
  }

  return (
    <>
      <QueryBoundary errorFallback="渠道治理详情加载失败" query={query}>
        {(channel) => (
          <>
            <PageHeader
              actions={
                <>
                  {channel.status === 'published' && (
                    <Button onClick={() => setAction({ kind: 'pause' })} variant="secondary">
                      暂停渠道
                    </Button>
                  )}
                  {channel.status !== 'deleted' && (
                    <Button onClick={() => setAction({ kind: 'delete' })} variant="danger">
                      删除渠道
                    </Button>
                  )}
                </>
              }
              back={
                <Link className="back-link" to="/admin/channels">
                  ← 渠道治理
                </Link>
              }
              title={
                <span className="admin-channel-title">
                  {channel.display_name} <AdminChannelBadge status={channel.status} />
                </span>
              }
            />
            <InlineError>{error}</InlineError>
            <SuccessMessage>{message}</SuccessMessage>
            <Card title="渠道概况">
              <dl className="admin-channel-facts">
                <div><dt>共享者</dt><dd>{channel.owner_display_name}</dd></div>
                <div><dt>凭据</dt><dd>{channel.credential_configured ? `已配置 · v${channel.credential_version}` : '未配置'}</dd></div>
                <div><dt>评分</dt><dd>{ratingText(channel.average_rating, channel.rating_count)}</dd></div>
                <div><dt>最近更新</dt><dd>{formatDate(channel.updated_at)}</dd></div>
              </dl>
            </Card>
            <Card flush title="协议报价">
              <DataTable
                caption="协议报价"
                columns={[
                  {
                    key: 'model',
                    header: '模型 / 协议',
                    primary: true,
                    cell: (offer) => (
                      <>
                        <strong>{offer.model_name}</strong>
                        <small>{protocolLabels[offer.protocol]}</small>
                      </>
                    ),
                  },
                  { key: 'multiplier', header: '倍率', numeric: true, cell: (offer) => `${offer.multiplier}×` },
                  { key: 'status', header: '状态', cell: (offer) => <AdminChannelBadge status={offer.status} /> },
                  {
                    key: 'validation',
                    header: '验证',
                    cell: (offer) =>
                      offer.latest_validation ? (
                        <>
                          <AdminChannelBadge status={offer.latest_validation.status} />
                          <small>{formatDate(offer.latest_validation.completed_at)}</small>
                        </>
                      ) : (
                        '待验证'
                      ),
                  },
                  {
                    key: 'actions',
                    header: '操作',
                    cell: (offer) => (
                      <span className="table-action-group">
                        <Button
                          onClick={() => setAction({ kind: 'validate', offer })}
                          size="sm"
                          variant="secondary"
                        >
                          重验
                        </Button>
                        <Button onClick={() => setHistoryOffer(offer)} size="sm" variant="quiet">
                          记录
                        </Button>
                      </span>
                    ),
                  },
                ]}
                empty={<EmptyState title="没有报价" />}
                rowKey={(offer) => offer.id}
                rows={channel.offers}
              />
            </Card>
            <Dialog
              busy={busy}
              description={action?.kind === 'validate' ? '将向上游发送最小请求。' : undefined}
              footer={
                <>
                  <Button disabled={busy} onClick={close} variant="secondary">
                    取消
                  </Button>
                  <Button
                    disabled={action?.kind === 'validate' ? !costConfirmed : !reason.trim()}
                    loading={busy}
                    onClick={() => void confirm(channel.version)}
                    variant={action?.kind === 'delete' ? 'danger' : 'primary'}
                  >
                    {action?.kind === 'validate' ? '开始验证' : actionTitles[action?.kind ?? 'pause']}
                  </Button>
                </>
              }
              onClose={close}
              open={action !== null}
              title={actionTitles[action?.kind ?? 'pause']}
            >
              {action?.kind === 'validate' ? (
                <Checkbox
                  checked={costConfirmed}
                  label="我确认可能产生少量上游费用"
                  onChange={(event) => setCostConfirmed(event.target.checked)}
                />
              ) : (
                <TextareaField
                  label="原因"
                  onChange={(event) => setReason(event.target.value)}
                  required
                  value={reason}
                />
              )}
            </Dialog>
            <AttemptsDrawer offer={historyOffer} onClose={() => setHistoryOffer(null)} />
          </>
        )}
      </QueryBoundary>
    </>
  )
}

function AttemptsDrawer({ offer, onClose }: { offer: AdminChannelOffer | null; onClose: () => void }) {
  const attempts = useValidationAttemptsQuery(offer?.id ?? '')
  return (
    <Drawer
      onClose={onClose}
      open={offer !== null}
      title={offer ? `验证记录 · ${offer.model_name}` : '验证记录'}
    >
      <QueryBoundary
        empty={<EmptyState title="暂无记录" />}
        errorFallback="验证记录加载失败"
        isEmpty={(items) => items.length === 0}
        query={attempts}
      >
        {(items) => (
          <ul className="admin-attempt-list">
            {items.map((attempt) => (
              <li key={attempt.id}>
                <header>
                  <AdminChannelBadge status={attempt.status} />
                  <span>{formatDate(attempt.completed_at ?? attempt.started_at)}</span>
                  <span>{attempt.http_status ? `HTTP ${attempt.http_status}` : attempt.error_category || '—'}</span>
                  <span>操作者 {attempt.actor_account_id}</span>
                </header>
                {attempt.raw_error && <pre>{attempt.raw_error}</pre>}
              </li>
            ))}
          </ul>
        )}
      </QueryBoundary>
    </Drawer>
  )
}
