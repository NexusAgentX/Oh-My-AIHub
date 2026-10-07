import { useState, type FormEvent } from 'react'
import { useParams } from 'react-router-dom'
import type { C2CTrade } from '../api/contracts'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'
import { formatPointAmount } from '../wallet/presentation'
import {
  Button,
  ButtonLink,
  Card,
  Dialog,
  InlineError,
  Notice,
  PageHeader,
  QueryBoundary,
  TextareaField,
  TextField,
} from '../ui'
import { C2CState } from './C2CState'
import { ConfirmDialog } from './ConfirmDialog'
import {
  c2cEventLabel,
  c2cPaymentLabels,
  c2cStatusTone,
  c2cTradeRole,
  c2cTradeStatusLabels,
  formatC2CDate,
  formatC2CFiat,
  formatC2CPrice,
  isC2CTradeTerminal,
} from './presentation'
import {
  useC2CTradeQuery,
  useCancelC2CTrade,
  useMarkC2CPaid,
  useReleaseC2CTrade,
  useSubmitC2CDispute,
} from './queries'

const statementLimit = 2000

function DisputeDialog({ trade, open, onClose }: { trade: C2CTrade; open: boolean; onClose: () => void }) {
  const submit = useSubmitC2CDispute()
  const [statement, setStatement] = useState('')
  const append = trade.status === 'disputed'
  const close = () => {
    submit.reset()
    onClose()
  }
  const onSubmit = (event: FormEvent) => {
    event.preventDefault()
    submit.mutate(
      { tradeID: trade.id, statement, append },
      {
        onSuccess: () => {
          setStatement('')
          close()
        },
      },
    )
  }
  return (
    <Dialog
      busy={submit.isPending}
      footer={
        <>
          <Button disabled={submit.isPending} onClick={close} type="button" variant="secondary">返回</Button>
          <Button form="c2c-dispute-form" loading={submit.isPending} type="submit">
            {append ? '补充陈述' : '提交争议'}
          </Button>
        </>
      }
      onClose={close}
      open={open}
      title={append ? '补充陈述' : '发起争议'}
    >
      <form className="c2c-dispute-form" id="c2c-dispute-form" onSubmit={onSubmit}>
        <TextareaField
          className="c2c-statement-input"
          hint={`${Array.from(statement).length} / ${statementLimit}`}
          label="情况说明（仅文字）"
          maxLength={statementLimit}
          onChange={(event) => setStatement(event.target.value)}
          required
          value={statement}
        />
        <InlineError>{submit.isError ? errorMessage(submit.error, '争议陈述提交失败') : ''}</InlineError>
      </form>
    </Dialog>
  )
}

function PaymentDetails({ trade }: { trade: C2CTrade }) {
  const method = trade.payment_method
  if (!method) return null
  return (
    <div className="c2c-payment-details">
      <h3>{trade.order_side === 'sell' ? '收款方式' : '买家联系方式'} · {c2cPaymentLabels[method.type]}</h3>
      {method.contact && <p><span>账号或联系方式</span><strong>{method.contact}</strong></p>}
      {method.instructions && <p><span>备注</span><strong>{method.instructions}</strong></p>}
      {method.qr_available && <img alt="收款码" src={method.qr_url} />}
    </div>
  )
}

type Dialogs = 'cancel' | 'release' | 'dispute' | null

function TradeActions({ trade, role }: { trade: C2CTrade; role: 'buyer' | 'seller' | null }) {
  const [dialog, setDialog] = useState<Dialogs>(null)
  const [reference, setReference] = useState('')
  const markPaid = useMarkC2CPaid()
  const cancel = useCancelC2CTrade()
  const release = useReleaseC2CTrade()
  const closeDialog = () => {
    cancel.reset()
    release.reset()
    setDialog(null)
  }

  const onPaid = (event: FormEvent) => {
    event.preventDefault()
    markPaid.mutate({ tradeID: trade.id, paymentReference: reference })
  }

  if (isC2CTradeTerminal(trade.status)) {
    return (
      <Notice tone={trade.status === 'released_to_buyer' ? 'success' : 'info'}>
        {c2cTradeStatusLabels[trade.status]}
        {trade.resolved_at ? ` · ${formatC2CDate(trade.resolved_at)}` : ''}
      </Notice>
    )
  }

  return (
    <>
      {trade.status === 'awaiting_payment' && role === 'buyer' && (
        <form className="c2c-actions" onSubmit={onPaid}>
          <TextField
            hint="文字声明，不上传截图；卖家核实到账后才会放行"
            label="付款备注或流水号（可选）"
            maxLength={256}
            onChange={(event) => setReference(event.target.value)}
            value={reference}
          />
          <InlineError>{markPaid.isError ? errorMessage(markPaid.error, '付款声明提交失败') : ''}</InlineError>
          <div className="c2c-action-row">
            <Button disabled={markPaid.isPending} onClick={() => setDialog('cancel')} type="button" variant="secondary">
              取消交易
            </Button>
            <Button loading={markPaid.isPending} type="submit">我已付款</Button>
          </div>
        </form>
      )}
      {trade.status === 'awaiting_payment' && role !== 'buyer' && (
        <Notice tone="info">等待买家付款，截止 {formatC2CDate(trade.payment_deadline)}</Notice>
      )}
      {trade.status === 'paid' && role === 'seller' && (
        <div className="c2c-actions">
          <Notice tone="warning">买家已声明付款。请在付款工具中确认人民币实际到账后再放行。</Notice>
          <div className="c2c-action-row">
            <Button onClick={() => setDialog('dispute')} type="button" variant="secondary">发起争议</Button>
            <Button onClick={() => setDialog('release')} type="button">确认收款并放行</Button>
          </div>
        </div>
      )}
      {trade.status === 'paid' && role === 'buyer' && (
        <div className="c2c-actions">
          <Notice tone="info">已声明付款，等待卖家确认到账</Notice>
          <div className="c2c-action-row">
            <Button onClick={() => setDialog('dispute')} type="button" variant="secondary">发起争议</Button>
          </div>
        </div>
      )}
      {trade.status === 'disputed' && (
        <div className="c2c-actions">
          <Notice tone="danger">争议处理中，积分保持冻结，等待管理员裁决</Notice>
          <div className="c2c-action-row">
            <Button onClick={() => setDialog('dispute')} type="button">补充陈述</Button>
          </div>
        </div>
      )}

      <ConfirmDialog
        busy={cancel.isPending}
        confirmLabel="取消交易"
        danger
        error={cancel.isError ? errorMessage(cancel.error, '交易取消失败') : ''}
        onClose={closeDialog}
        onConfirm={() => cancel.mutate(trade.id, { onSuccess: closeDialog })}
        open={dialog === 'cancel'}
        title="取消交易"
      >
        取消后数量将返回挂单，且无法恢复这笔交易。
      </ConfirmDialog>
      <ConfirmDialog
        busy={release.isPending}
        confirmLabel="确认放行"
        error={release.isError ? errorMessage(release.error, '放行失败') : ''}
        onClose={closeDialog}
        onConfirm={() => release.mutate(trade.id, { onSuccess: closeDialog })}
        open={dialog === 'release'}
        title="确认收款并放行"
      >
        放行后积分立即划转给买家且不可撤销。请确认已在付款工具中看到 {formatC2CFiat(trade.fiat_amount_fen)} 实际到账。
      </ConfirmDialog>
      <DisputeDialog onClose={closeDialog} open={dialog === 'dispute'} trade={trade} />
    </>
  )
}

function TradeDetail({ trade, accountID }: { trade: C2CTrade; accountID: string }) {
  const role = c2cTradeRole(trade, accountID)
  const counterparty = role === 'buyer' ? trade.seller_display_name : trade.buyer_display_name
  return (
    <>
      <div className="c2c-trade-layout">
        <Card className="c2c-trade-main" title={role === 'buyer' ? '购买积分' : '出售积分'}>
          <dl className="c2c-trade-values">
            <div><dt>数量</dt><dd className="num">{formatPointAmount(trade.quantity)} 积分</dd></div>
            <div><dt>单价</dt><dd className="num">{formatC2CPrice(trade.unit_price_fen)}</dd></div>
            <div className="c2c-trade-total"><dt>人民币</dt><dd className="num">{formatC2CFiat(trade.fiat_amount_fen)}</dd></div>
            <div><dt>对方</dt><dd>{counterparty}</dd></div>
          </dl>
          <PaymentDetails trade={trade} />
          <TradeActions role={role} trade={trade} />
        </Card>

        <Card className="c2c-trade-meta" flush title="时间">
          <dl className="detail-list">
            <div><dt>创建</dt><dd>{formatC2CDate(trade.created_at)}</dd></div>
            <div><dt>付款截止</dt><dd>{formatC2CDate(trade.payment_deadline)}</dd></div>
            {trade.paid_at && <div><dt>声明付款</dt><dd>{formatC2CDate(trade.paid_at)}</dd></div>}
            {trade.review_due_at && <div><dt>复核期限</dt><dd>{formatC2CDate(trade.review_due_at)}</dd></div>}
            {trade.payment_reference && <div><dt>付款备注</dt><dd className="c2c-wrap">{trade.payment_reference}</dd></div>}
          </dl>
        </Card>
      </div>

      {trade.statements.length > 0 && (
        <Card className="c2c-section" title="争议陈述">
          <div className="c2c-statement-list">
            {trade.statements.map((statement) => (
              <article key={statement.id}>
                <header>
                  <strong>{statement.actor_display_name}</strong>
                  <span>{formatC2CDate(statement.created_at)}</span>
                </header>
                <p>{statement.deleted_at ? '内容已按保留期清理' : statement.text}</p>
              </article>
            ))}
          </div>
        </Card>
      )}

      <Card className="c2c-section" title="交易记录">
        <ol className="c2c-events">
          {trade.events.map((event) => (
            <li key={event.id}>
              <span>{formatC2CDate(event.created_at)}</span>
              <strong>{c2cEventLabel(event)}</strong>
            </li>
          ))}
        </ol>
      </Card>
    </>
  )
}

export function C2CTradePage() {
  const { tradeID = '' } = useParams()
  const { account } = useAuth()
  const query = useC2CTradeQuery(tradeID)
  const trade = query.data
  return (
    <>
      <PageHeader
        actions={trade && <C2CState label={c2cTradeStatusLabels[trade.status]} tone={c2cStatusTone(trade.status)} />}
        back={<ButtonLink size="sm" to="/c2c/me" variant="quiet">← 我的 C2C</ButtonLink>}
        title="交易详情"
      />
      <QueryBoundary errorFallback="交易加载失败" query={query}>
        {(data) => <TradeDetail accountID={account?.id ?? ''} trade={data} />}
      </QueryBoundary>
    </>
  )
}
