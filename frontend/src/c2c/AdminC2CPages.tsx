import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import type { C2CTrade } from '../api/contracts'
import { errorMessage } from '../api/query'
import {
  Badge,
  Button,
  ButtonLink,
  Card,
  CountBadge,
  DataTable,
  Dialog,
  EmptyState,
  InlineError,
  Metric,
  MetricGrid,
  PageHeader,
  QueryBoundary,
  TextField,
} from '../ui'
import { formatPointAmount } from '../wallet/presentation'
import {
  useAdminDisputeQuery,
  useAdminDisputesQuery,
  useDisputeOperation,
  type DisputeOperation,
} from './adminQueries'
import {
  c2cDisputeParties,
  c2cStatusTone,
  c2cTradeStatusLabels,
  canRestrictC2CParty,
  formatC2CDate,
  formatC2CFiat,
} from './presentation'

export function AdminC2CDisputesPage() {
  const query = useAdminDisputesQuery()
  return (
    <>
      <PageHeader
        actions={query.data && <CountBadge>{query.data.length}</CountBadge>}
        title="争议处理"
      />
      <Card flush>
        <QueryBoundary errorFallback="争议列表加载失败" query={query}>
          {(trades) => (
            <DataTable
              caption="待处理争议"
              columns={[
                {
                  key: 'trade',
                  header: '交易',
                  primary: true,
                  cell: (trade) => (
                    <>
                      <strong>#{trade.id.slice(0, 8)}</strong>
                      <small>{formatC2CDate(trade.updated_at)}</small>
                    </>
                  ),
                },
                { key: 'buyer', header: '买家', cell: (trade) => trade.buyer_display_name },
                { key: 'seller', header: '卖家', cell: (trade) => trade.seller_display_name },
                { key: 'quantity', header: '数量', numeric: true, cell: (trade) => formatPointAmount(trade.quantity) },
                { key: 'fiat', header: '人民币', numeric: true, cell: (trade) => formatC2CFiat(trade.fiat_amount_fen) },
                { key: 'due', header: '复核期限', cell: (trade) => formatC2CDate(trade.review_due_at) },
                {
                  key: 'actions',
                  header: '操作',
                  cell: (trade) => (
                    <ButtonLink size="sm" to={`/admin/c2c/disputes/${trade.id}`} variant="primary">
                      处理
                    </ButtonLink>
                  ),
                },
              ]}
              empty={<EmptyState title="暂无待处理争议" />}
              rowKey={(trade) => trade.id}
              rows={trades}
            />
          )}
        </QueryBoundary>
      </Card>
    </>
  )
}

const toneMap = { positive: 'success', danger: 'danger', neutral: 'neutral', warning: 'warning' } as const

function statusTone(status: C2CTrade['status']) {
  return toneMap[c2cStatusTone(status) as keyof typeof toneMap]
}

const operationLabels: Record<string, string> = {
  release_to_buyer: '放行给买家',
  return_to_seller: '退还给卖家',
  extend_review: '延长复核',
  restrict_buyer: '限制买家',
  restrict_seller: '限制卖家',
  'cancel-order': '取消剩余挂单',
}

function operationKey(operation: DisputeOperation) {
  return operation.kind === 'resolve' ? operation.action : operation.kind
}

export function AdminC2CDisputePage() {
  const { tradeID = '' } = useParams()
  const query = useAdminDisputeQuery(tradeID)
  const run = useDisputeOperation(tradeID)
  const [reason, setReason] = useState('')
  const [pending, setPending] = useState<DisputeOperation | null>(null)
  const [error, setError] = useState('')

  const request = (operation: DisputeOperation) => {
    if (!reason.trim()) {
      setError('请填写裁决原因')
      return
    }
    setError('')
    setPending(operation)
  }

  const confirm = async () => {
    if (!pending) return
    try {
      await run.mutateAsync({ operation: pending, reason })
      setError('')
    } catch (caught) {
      setError(errorMessage(caught, '管理员操作失败'))
    }
    setPending(null)
  }

  const busy = run.isPending

  return (
    <QueryBoundary errorFallback="争议详情加载失败" query={query}>
      {(trade) => (
        <>
          <PageHeader
            actions={
              <Badge tone={statusTone(trade.status)}>
                {c2cTradeStatusLabels[trade.status]}
              </Badge>
            }
            back={
              <Link className="back-link" to="/admin/c2c/disputes">
                ← 争议处理
              </Link>
            }
            title="争议详情"
          />
          <MetricGrid label="争议交易摘要">
            <Metric hint="积分" label="数量" value={formatPointAmount(trade.quantity)} />
            <Metric hint="外部支付" label="人民币" tone="warm" value={formatC2CFiat(trade.fiat_amount_fen)} />
            <Metric hint={trade.buyer_account_id.slice(0, 8)} label="买家" value={trade.buyer_display_name} />
            <Metric hint={trade.seller_account_id.slice(0, 8)} label="卖家" value={trade.seller_display_name} />
          </MetricGrid>

          <div className="admin-dispute-layout">
            <Card flush title="双方陈述">
              <StatementList trade={trade} />
            </Card>

            <Card title="仲裁处理">
              <div className="admin-dispute-form">
                <InlineError>{error}</InlineError>
                <TextField
                  label="处理原因"
                  maxLength={512}
                  onChange={(event) => setReason(event.target.value)}
                  required
                  value={reason}
                />
                <div aria-label="账户处置" className="admin-dispute-parties" role="group">
                  <span className="field-label">账户处置</span>
                  {c2cDisputeParties(trade).map((party) => (
                    <div className="admin-dispute-party" key={party.role}>
                      <div>
                        <strong>
                          {party.label} · {party.displayName}
                        </strong>
                        {party.creditFrozen !== undefined && (
                          <Badge tone={party.creditFrozen ? 'danger' : 'neutral'}>
                            {party.creditFrozen ? '信用已冻结' : '信用正常'}
                          </Badge>
                        )}
                      </div>
                      <div>
                        <Button
                          disabled={busy || party.creditFrozen === true || !canRestrictC2CParty(trade.status)}
                          onClick={() => request({ kind: 'resolve', action: party.restrictAction })}
                          size="sm"
                          variant="danger"
                        >
                          限制{party.label}
                        </Button>
                        <ButtonLink size="sm" to={`/admin/accounts?query=${encodeURIComponent(party.accountID)}`}>
                          管理账户
                        </ButtonLink>
                      </div>
                    </div>
                  ))}
                </div>
                <div className="admin-dispute-actions">
                  <Button disabled={busy} onClick={() => request({ kind: 'resolve', action: 'release_to_buyer' })}>
                    放行给买家
                  </Button>
                  <Button disabled={busy} onClick={() => request({ kind: 'resolve', action: 'return_to_seller' })} variant="secondary">
                    退还给卖家
                  </Button>
                  <Button
                    disabled={busy || trade.status !== 'disputed'}
                    onClick={() => request({ kind: 'resolve', action: 'extend_review' })}
                    variant="secondary"
                  >
                    延长复核
                  </Button>
                  <Button disabled={busy} onClick={() => request({ kind: 'cancel-order', orderID: trade.order_id })} variant="danger">
                    取消剩余挂单
                  </Button>
                </div>
              </div>
            </Card>
          </div>

          <Dialog
            busy={busy}
            description={pending && pending.kind === 'resolve' && pending.action.startsWith('restrict_')
              ? '冻结后无法发起新调用、挂单或接单，本交易状态不变。'
              : '提交后将写入审计记录，无法撤销。'}
            footer={
              <>
                <Button disabled={busy} onClick={() => setPending(null)} variant="secondary">
                  取消
                </Button>
                <Button loading={busy} onClick={() => void confirm()} variant="danger">
                  确认
                </Button>
              </>
            }
            onClose={() => setPending(null)}
            open={pending !== null}
            title={pending ? `确认${operationLabels[operationKey(pending)]}` : ''}
          >
            <p className="muted-copy">原因：{reason}</p>
          </Dialog>
        </>
      )}
    </QueryBoundary>
  )
}

function StatementList({ trade }: { trade: C2CTrade }) {
  if (trade.statements.length === 0) return <EmptyState title="暂无陈述" />
  return (
    <div className="admin-statement-list">
      {trade.statements.map((item) => (
        <article key={item.id}>
          <header>
            <strong>{item.actor_display_name}</strong>
            <span>{formatC2CDate(item.created_at)}</span>
          </header>
          <p>{item.deleted_at ? '内容已按保留期清理' : item.text}</p>
        </article>
      ))}
    </div>
  )
}
