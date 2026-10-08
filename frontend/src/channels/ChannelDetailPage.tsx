import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import type { AuthorizedValidationAttempt, Channel, ChannelOffer } from '../api/types'
import { errorMessage } from '../api/query'
import { formatDate, formatPoints, formatRate, PricePair, protocolLabels, ratingText } from '../gateway/presentation'
import {
  Badge,
  Button,
  ButtonLink,
  Card,
  Checkbox,
  DataTable,
  Drawer,
  EmptyState,
  InlineError,
  Metric,
  MetricGrid,
  Notice,
  PageHeader,
  QueryBoundary,
  Tabs,
  type Column,
} from '../ui'
import {
  ChannelStateBadge,
  ConfirmActionDialog,
  eligibilityLabel,
  TierCountBadge,
  TierPriceList,
} from './presentation'
import {
  useChannelQuery,
  useChannelStatusMutation,
  useDeleteChannelMutation,
  useDeleteOfferMutation,
  useOfferStatusMutation,
  useRevokeCredentialMutation,
  useValidateOfferMutation,
  useValidationAttemptsQuery,
} from './queries'
import { summarizeChannel } from './summary'

type Pending =
  | { kind: 'validate' | 'delete-offer'; offer: ChannelOffer }
  | { kind: 'publish' | 'pause' | 'delete-channel' | 'revoke-credential' }

type DetailTab = 'offers' | 'metrics'

const pendingTitles: Record<Pending['kind'], string> = {
  validate: '校验报价',
  'delete-offer': '删除报价',
  publish: '发布渠道',
  pause: '暂停渠道',
  'delete-channel': '删除渠道',
  'revoke-credential': '撤销平台凭据',
}

const pendingDescriptions: Partial<Record<Pending['kind'], string>> = {
  validate: '将向上游发送一次最小请求。',
  'delete-offer': '报价删除后不再进入市场与模型池。',
  pause: '暂停后渠道下的报价不再接收新调用。',
  'delete-channel': '渠道及其全部报价将被删除，此操作不可撤销。',
  'revoke-credential': '撤销后平台不再保存上游凭据，需重新填写才能继续使用。',
}

export function ChannelDetailPage() {
  const { channelID = '' } = useParams()
  const query = useChannelQuery(channelID)
  return (
    <QueryBoundary errorFallback="渠道加载失败" query={query}>
      {(channel) => <ChannelDetail channel={channel} key={channel.id} />}
    </QueryBoundary>
  )
}

function ChannelDetail({ channel }: { channel: Channel }) {
  const navigate = useNavigate()
  const [tab, setTab] = useState<DetailTab>('offers')
  const [pending, setPending] = useState<Pending | null>(null)
  const [costConfirmed, setCostConfirmed] = useState(false)
  const [dialogError, setDialogError] = useState('')
  const [pageError, setPageError] = useState('')
  const [historyOffer, setHistoryOffer] = useState<ChannelOffer | null>(null)
  const [validationResult, setValidationResult] = useState<AuthorizedValidationAttempt | null>(null)

  const statusMutation = useChannelStatusMutation()
  const deleteChannel = useDeleteChannelMutation()
  const revokeCredential = useRevokeCredentialMutation()
  const offerStatus = useOfferStatusMutation()
  const deleteOffer = useDeleteOfferMutation()
  const validate = useValidateOfferMutation()
  const busy = [statusMutation, deleteChannel, revokeCredential, offerStatus, deleteOffer, validate]
    .some((mutation) => mutation.isPending)

  const summary = summarizeChannel(channel)
  const deleted = channel.status === 'deleted'
  const canPublish = channel.status === 'draft' || channel.status === 'paused'

  const closeDialog = () => {
    if (busy) return
    setPending(null)
    setCostConfirmed(false)
    setDialogError('')
  }

  const openDialog = (next: Pending) => {
    setDialogError('')
    setCostConfirmed(false)
    setPending(next)
  }

  const confirm = async () => {
    if (!pending) return
    setDialogError('')
    try {
      switch (pending.kind) {
        case 'validate': {
          const result = await validate.mutateAsync(pending.offer)
          setValidationResult(result)
          setHistoryOffer(pending.offer)
          break
        }
        case 'delete-offer':
          await deleteOffer.mutateAsync(pending.offer)
          break
        case 'revoke-credential':
          await revokeCredential.mutateAsync(channel)
          break
        case 'delete-channel':
          await deleteChannel.mutateAsync(channel)
          navigate('/channels', { replace: true })
          return
        default:
          await statusMutation.mutateAsync({ channel, action: pending.kind })
      }
      setPending(null)
      setCostConfirmed(false)
    } catch (caught) {
      setDialogError(errorMessage(caught, '操作失败'))
    }
  }

  const toggleOffer = async (offer: ChannelOffer) => {
    setPageError('')
    try {
      await offerStatus.mutateAsync(offer)
    } catch (caught) {
      setPageError(errorMessage(caught, '报价状态更新失败'))
    }
  }

  const openHistory = (offer: ChannelOffer) => {
    setValidationResult(null)
    setHistoryOffer(offer)
  }

  const offerLabel = (offer: ChannelOffer) => `${offer.model_name} ${protocolLabels[offer.protocol]}`

  const modelCell = (offer: ChannelOffer) => (
    <span className="sharing-name">
      <strong>{offer.model_name}</strong>
      <small>{protocolLabels[offer.protocol]} · {offer.upstream_model_id}</small>
    </span>
  )

  const offerColumns: Column<ChannelOffer>[] = [
    { key: 'model', header: '模型 / 协议', primary: true, cell: modelCell },
    { key: 'multiplier', header: '倍率', numeric: true, cell: (offer) => `${offer.multiplier}×` },
    {
      key: 'price',
      header: '输入 / 输出',
      cell: (offer) => (
        <>
          <span className="sharing-price"><PricePair first={offer.input_price} second={offer.output_price} /><TierCountBadge tiers={offer.price_tiers} /></span>
          <TierPriceList tiers={offer.price_tiers} />
        </>
      ),
    },
    { key: 'cache', header: '缓存写 / 读', cell: (offer) => <PricePair first={offer.cache_write_price} second={offer.cache_read_price} /> },
    {
      key: 'validation',
      header: '校验',
      cell: (offer) => (
        <span className="sharing-name">
          {offer.latest_validation ? <ChannelStateBadge status={offer.latest_validation.status} /> : <Badge>待校验</Badge>}
          <small>{offer.eligible ? '当前可用' : eligibilityLabel(offer.ineligible_reason)}</small>
          {offer.latest_validation && <small>{formatDate(offer.latest_validation.completed_at)}</small>}
        </span>
      ),
    },
    ...(deleted ? [] : [{
      key: 'actions',
      header: '操作',
      cell: (offer: ChannelOffer) => (
        <span className="sharing-actions">
          <Button
            aria-label={`校验 ${offerLabel(offer)}`}
            disabled={busy || !channel.credential_configured}
            onClick={() => openDialog({ kind: 'validate', offer })}
            size="sm"
            variant="secondary"
          >校验</Button>
          <Button aria-label={`校验记录 ${offerLabel(offer)}`} onClick={() => openHistory(offer)} size="sm" variant="quiet">记录</Button>
          <Button
            aria-label={`${offer.status === 'active' ? '停用' : '启用'} ${offerLabel(offer)}`}
            disabled={busy}
            onClick={() => void toggleOffer(offer)}
            size="sm"
            variant="quiet"
          >{offer.status === 'active' ? '停用' : '启用'}</Button>
          <Button
            aria-label={`删除 ${offerLabel(offer)}`}
            disabled={busy}
            onClick={() => openDialog({ kind: 'delete-offer', offer })}
            size="sm"
            variant="quiet"
          >删除</Button>
        </span>
      ),
    }]),
  ]

  const metricColumns: Column<ChannelOffer>[] = [
    { key: 'model', header: '模型 / 协议', primary: true, cell: modelCell },
    { key: 'calls', header: '调用', numeric: true, cell: (offer) => offer.call_count ?? 0 },
    { key: 'rate', header: '成功率', numeric: true, cell: (offer) => (offer.call_success_rate == null ? '—' : formatRate(offer.call_success_rate)) },
    { key: 'ttft', header: 'TTFT', numeric: true, cell: (offer) => (offer.ttft_milliseconds == null ? '—' : `${offer.ttft_milliseconds} ms`) },
    { key: 'tps', header: 'TPS', numeric: true, cell: (offer) => (offer.tokens_per_second == null ? '—' : `${offer.tokens_per_second} tok/s`) },
    { key: 'income', header: '收入', numeric: true, cell: (offer) => (offer.provider_income ? `${formatPoints(offer.provider_income)} 积分` : '—') },
  ]

  const empty = (
    <EmptyState
      action={deleted ? undefined : <ButtonLink to={`/channels/${channel.id}/settings`}>编辑配置</ButtonLink>}
      title="没有报价"
    />
  )

  const dangerPending = pending?.kind === 'delete-channel' || pending?.kind === 'delete-offer' || pending?.kind === 'revoke-credential'

  return (
    <>
      <PageHeader
        actions={deleted ? undefined : (
          <>
            <ButtonLink to={`/channels/${channel.id}/settings`}>编辑配置</ButtonLink>
            {canPublish && <Button disabled={busy} onClick={() => openDialog({ kind: 'publish' })}>发布</Button>}
            {channel.status === 'published' && <Button disabled={busy} onClick={() => openDialog({ kind: 'pause' })} variant="secondary">暂停</Button>}
          </>
        )}
        back={<Link className="back-link" to="/channels">← 我的渠道</Link>}
        title={<span className="sharing-title">{channel.display_name}<ChannelStateBadge status={channel.status} /></span>}
      />
      <InlineError>{pageError}</InlineError>
      {deleted && <Notice tone="danger">渠道已删除，仅可查看历史数据。</Notice>}
      {channel.status === 'published' && summary.eligibleCount === 0 && (
        <Notice tone="warning">暂无可用报价，渠道不会出现在市场。</Notice>
      )}
      <MetricGrid label="渠道指标">
        <Metric hint={summary.successRate === null ? '暂无调用' : `成功率 ${formatRate(summary.successRate)}`} label="调用" value={summary.calls} />
        <Metric
          hint={summary.incomeIncludesDeleted ? '积分 · 含已删除报价' : '积分'}
          label="收入"
          tone="accent"
          value={summary.income === null ? '—' : formatPoints(summary.income)}
        />
        <Metric hint={`已启用 ${summary.enabledCount} / ${summary.offers.length}`} label="可用报价" value={summary.eligibleCount} />
        <Metric label="评分" value={channel.average_rating ?? '—'} hint={ratingText(channel.average_rating, channel.rating_count)} />
      </MetricGrid>

      <Card flush>
        <Tabs
          items={[
            { key: 'offers', label: '报价', count: summary.offers.length },
            { key: 'metrics', label: '调用与收入' },
          ]}
          label="渠道内容"
          onChange={setTab}
          value={tab}
        >
          <DataTable
            caption={tab === 'offers' ? '渠道报价' : '报价调用与收入'}
            columns={tab === 'offers' ? offerColumns : metricColumns}
            empty={empty}
            rowKey={(offer) => offer.id}
            rows={summary.offers}
          />
        </Tabs>
      </Card>

      <Card title="连接">
        <dl className="sharing-facts">
          <div><dt>Base URL</dt><dd className="break-value">{channel.base_url}</dd></div>
          <div><dt>上游凭据</dt><dd>{channel.credential_configured ? `已配置 · v${channel.credential_version}` : '未配置'}</dd></div>
          <div><dt>最近更新</dt><dd>{formatDate(channel.updated_at)}</dd></div>
        </dl>
      </Card>

      {!deleted && (
        <Card
          actions={(
            <>
              {channel.credential_configured && (
                <Button disabled={busy} onClick={() => openDialog({ kind: 'revoke-credential' })} size="sm" variant="secondary">撤销凭据</Button>
              )}
              <Button disabled={busy} onClick={() => openDialog({ kind: 'delete-channel' })} size="sm" variant="danger">删除渠道</Button>
            </>
          )}
          className="sharing-danger"
          title="危险操作"
        >
          <p className="muted">撤销凭据与删除渠道均不可撤销。</p>
        </Card>
      )}

      <Drawer
        onClose={() => setHistoryOffer(null)}
        open={Boolean(historyOffer)}
        title="校验记录"
        description={historyOffer ? `${historyOffer.model_name} · ${protocolLabels[historyOffer.protocol]}` : undefined}
      >
        {validationResult && (
          <Notice tone={validationResult.status === 'passed' ? 'success' : 'danger'}>
            {validationResult.status === 'passed' ? '校验通过' : '校验失败'}
          </Notice>
        )}
        {historyOffer && <ValidationHistory offerID={historyOffer.id} />}
      </Drawer>

      <ConfirmActionDialog
        busy={busy}
        confirmDisabled={pending?.kind === 'validate' && !costConfirmed}
        confirmLabel={pending?.kind === 'validate' ? '开始校验' : '确认'}
        danger={dangerPending}
        description={pending ? pendingDescriptions[pending.kind] : undefined}
        error={dialogError}
        onCancel={closeDialog}
        onConfirm={() => void confirm()}
        open={Boolean(pending)}
        title={pending ? pendingTitles[pending.kind] : ''}
      >
        {pending?.kind === 'validate' && (
          <Checkbox
            checked={costConfirmed}
            label="我确认可能产生少量上游费用"
            onChange={(event) => setCostConfirmed(event.target.checked)}
          />
        )}
      </ConfirmActionDialog>
    </>
  )
}

function ValidationHistory({ offerID }: { offerID: string }) {
  const query = useValidationAttemptsQuery(offerID)
  return (
    <QueryBoundary
      empty={<EmptyState title="暂无记录" />}
      errorFallback="校验记录加载失败"
      isEmpty={(attempts) => attempts.length === 0}
      query={query}
    >
      {(attempts) => (
        <ol className="sharing-history">
          {attempts.map((attempt) => (
            <li key={attempt.id}>
              <header>
                <ChannelStateBadge status={attempt.status} />
                <span>{formatDate(attempt.completed_at ?? attempt.started_at)}</span>
                <span>{attempt.http_status ? `HTTP ${attempt.http_status}` : attempt.error_category || '—'}</span>
              </header>
              {attempt.raw_error && <pre>{attempt.raw_error}</pre>}
            </li>
          ))}
        </ol>
      )}
    </QueryBoundary>
  )
}
