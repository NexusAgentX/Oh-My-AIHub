import { useEffect, useState } from 'react'
import { errorMessage } from '../api/query'
import type { C2CTrade } from '../api/types'
import { formatFen, formatPoints } from '../money/format'
import { formatCountdown } from '../money/time'
import { Badge, Button, ConfirmDialog, CopyButton, InlineError, TextareaField } from '../ui'
import { tradeStatusLabels } from './c2c'
import { useTradeAction } from './queries'

export function TradeStatusBadge({ status }: { status: C2CTrade['status'] }) {
  return <Badge tone={tradeStatusLabels[status].tone}>{tradeStatusLabels[status].label}</Badge>
}

/** 付款倒计时（秒），到期后为 0。 */
export function useCountdown(deadline: string) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])
  return Math.max(0, (new Date(deadline).getTime() - now) / 1000)
}

/** 收款信息：方式 + 文字账号（只对交易双方可见）、备注交易号与应付金额。 */
export function PaymentInfo({ trade }: { trade: C2CTrade }) {
  const remark = trade.id.slice(0, 8)
  return (
    <div className="payment-info">
      <dl className="payment-amount">
        <div>
          <dt>应付</dt>
          <dd className="num">{formatFen(trade.total_fen)}</dd>
        </div>
        <div>
          <dt>获得</dt>
          <dd className="num">{formatPoints(trade.amount)} 积分</dd>
        </div>
      </dl>
      <div className="payment-remark">
        <span>付款备注交易号</span>
        <code className="mono">{remark}</code>
        <CopyButton value={remark} />
      </div>
      {trade.payment_methods.length > 0 ? (
        <ul className="payment-methods">
          {trade.payment_methods.map((method, index) => (
            // 收款方式没有独立 ID，按位置作为 key
            <li key={index}>
              <strong>{method.channel}</strong>
              <span className="mono">{method.account}</span>
              <CopyButton value={method.account} />
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted-copy">收款信息不可见</p>
      )}
    </div>
  )
}

export function PaymentCountdown({ deadline }: { deadline: string }) {
  const left = useCountdown(deadline)
  return (
    <span className={`countdown num ${left < 300 ? 'countdown-urgent' : ''}`} role="timer">
      {left > 0 ? `剩余 ${formatCountdown(left)}` : '已超时'}
    </span>
  )
}

type Confirm = 'release' | 'dispute' | 'cancel' | null

/** 按状态与角色的操作：已付款 / 确认收款并放行 / 取消 / 申诉。放行、申诉与取消需确认。 */
export function TradeActions({ trade, onChanged }: { trade: C2CTrade; onChanged?: (trade: C2CTrade) => void }) {
  const action = useTradeAction()
  const [note, setNote] = useState('')
  const [statement, setStatement] = useState('')
  const [confirm, setConfirm] = useState<Confirm>(null)
  const buyer = trade.viewer_role === 'buyer'
  const seller = trade.viewer_role === 'seller'
  const run = (kind: 'paid' | 'release' | 'cancel' | 'dispute', text?: string) =>
    action.mutate(
      { id: trade.id, action: kind, text },
      {
        onSuccess: (updated) => {
          setConfirm(null)
          onChanged?.(updated)
        },
      },
    )
  const error = action.isError ? errorMessage(action.error, '操作失败，请重试') : ''
  const canDispute = (trade.status === 'paid' && (buyer || seller)) || (trade.status === 'disputed' && !(buyer ? trade.buyer_statement : trade.seller_statement))

  return (
    <div className="trade-actions">
      {buyer && trade.status === 'awaiting_payment' && (
        <>
          <TextareaField label="付款说明（可选）" maxLength={500} onChange={(event) => setNote(event.target.value)} rows={2} value={note} />
          <div className="form-actions">
            <Button onClick={() => setConfirm('cancel')} type="button" variant="secondary">
              取消交易
            </Button>
            <Button loading={action.isPending && action.variables?.action === 'paid'} onClick={() => run('paid', note.trim() || undefined)} type="button">
              我已付款
            </Button>
          </div>
        </>
      )}
      {seller && trade.status === 'paid' && (
        <div className="form-actions">
          <Button onClick={() => setConfirm('release')} type="button">
            确认收款并放行
          </Button>
        </div>
      )}
      {canDispute && (
        <div className="form-actions">
          <Button onClick={() => setConfirm('dispute')} type="button" variant="danger">
            {trade.status === 'disputed' ? '提交申诉陈述' : '申诉'}
          </Button>
        </div>
      )}
      {!confirm && <InlineError>{error}</InlineError>}
      <ConfirmDialog
        busy={action.isPending}
        confirmLabel="放行"
        description={`${formatPoints(trade.amount)} 积分将转给 ${trade.buyer.display_name}，无法撤回`}
        error={error}
        onClose={() => setConfirm(null)}
        onConfirm={() => run('release')}
        open={confirm === 'release'}
        title="确认已收到付款？"
      />
      <ConfirmDialog
        busy={action.isPending}
        confirmLabel="取消交易"
        danger
        error={error}
        onClose={() => setConfirm(null)}
        onConfirm={() => run('cancel')}
        open={confirm === 'cancel'}
        title="取消这笔交易？"
      />
      <ConfirmDialog
        busy={action.isPending}
        confirmLabel="提交申诉"
        danger
        description="管理员会根据双方陈述仲裁"
        error={error}
        onClose={() => setConfirm(null)}
        onConfirm={() => statement.trim() && run('dispute', statement.trim())}
        open={confirm === 'dispute'}
        title="申诉"
      >
        <TextareaField label="陈述" maxLength={2000} onChange={(event) => setStatement(event.target.value)} required rows={4} value={statement} />
      </ConfirmDialog>
    </div>
  )
}
