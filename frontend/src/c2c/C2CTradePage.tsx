import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { C2CTrade } from '../api/contracts'
import { useAuth } from '../auth/AuthProvider'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState, TextField } from '../ui/FormControls'
import { formatPointAmount } from '../wallet/presentation'
import { C2CState } from './C2CActivityPage'
import {
  c2cPaymentLabels,
  c2cStatusTone,
  c2cTradeStatusLabels,
  formatC2CDate,
  formatC2CFiat,
  formatC2CPrice,
  isC2CTradeTerminal,
} from './presentation'

export function C2CTradePage() {
  const { tradeID = '' } = useParams()
  const { account } = useAuth()
  const [trade, setTrade] = useState<C2CTrade | null>(null)
  const [paymentReference, setPaymentReference] = useState('')
  const [screenshot, setScreenshot] = useState<File | null>(null)
  const [loading, setLoading] = useState(true)
  const [acting, setActing] = useState('')
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try { setTrade(await api.c2cTrade(tradeID)) } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '交易加载失败')
    } finally { setLoading(false) }
  }, [tradeID])

  useEffect(() => { void load() }, [load])

  const buyer = trade?.buyer_account_id === account?.id
  const seller = trade?.seller_account_id === account?.id
  const counterparty = useMemo(() => !trade ? '' : buyer ? trade.seller_display_name : trade.buyer_display_name, [buyer, trade])

  const run = async (name: string, operation: () => Promise<unknown>) => {
    setActing(name)
    setError('')
    try { await operation(); await load() } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '交易操作失败')
    } finally { setActing('') }
  }

  const markPaid = (event: FormEvent) => {
    event.preventDefault()
    void run('paid', () => api.markC2CPaid(tradeID, paymentReference, screenshot))
  }

  const cancel = () => {
    if (!window.confirm('取消这笔交易？')) return
    void run('cancel', () => api.cancelC2CTrade(tradeID))
  }

  const release = () => {
    if (!window.confirm('已在付款工具中确认人民币实际到账，并立即放行积分？')) return
    void run('release', () => api.releaseC2CTrade(tradeID))
  }

  return (
    <AppShell>
      <header className="page-heading c2c-page-heading">
        <div><Link className="back-link" to="/c2c/me">← 我的 C2C</Link><h1>交易详情</h1></div>
        {trade && <C2CState label={c2cTradeStatusLabels[trade.status]} tone={c2cStatusTone(trade.status)} />}
      </header>
      <InlineError>{error}</InlineError>
      {loading && !trade ? <LoadingState /> : trade ? (
        <>
          <div className="c2c-trade-layout">
            <section className="panel c2c-trade-main">
              <header className="panel-heading"><h2>{buyer ? '购买积分' : '出售积分'}</h2><span className="muted-copy">#{trade.id.slice(0, 8)}</span></header>
              <dl className="c2c-trade-values">
                <div><dt>数量</dt><dd>{formatPointAmount(trade.quantity)} 积分</dd></div>
                <div><dt>单价</dt><dd>{formatC2CPrice(trade.unit_price_fen)}</dd></div>
                <div className="c2c-trade-total"><dt>人民币</dt><dd>{formatC2CFiat(trade.fiat_amount_fen)}</dd></div>
                <div><dt>对方</dt><dd>{counterparty}</dd></div>
              </dl>

              {trade.payment_method && (
                <section className="c2c-payment-details">
                  <header><h3>{c2cPaymentLabels[trade.payment_method.type]}</h3></header>
                  {trade.payment_method.contact && <div><span>账号或联系方式</span><strong>{trade.payment_method.contact}</strong></div>}
                  {trade.payment_method.instructions && <div><span>备注</span><strong>{trade.payment_method.instructions}</strong></div>}
                  {trade.payment_method.qr_available && <img alt="收款码" src={trade.payment_method.qr_url} />}
                </section>
              )}

              {trade.status === 'awaiting_payment' && buyer && (
                <form className="c2c-paid-form" onSubmit={markPaid}>
                  <TextField label="付款备注或流水号（可选）" maxLength={256} onChange={(event) => setPaymentReference(event.target.value)} value={paymentReference} />
                  <label className="field"><span className="field-label">付款截图（可选）</span><input accept="image/jpeg,image/png" className="input c2c-file-input" onChange={(event) => setScreenshot(event.target.files?.[0] ?? null)} type="file" /></label>
                  <div className="form-actions"><Button disabled={Boolean(acting)} onClick={cancel} type="button" variant="secondary">取消交易</Button><Button disabled={Boolean(acting)} type="submit">{acting === 'paid' ? '正在提交' : '我已付款'}</Button></div>
                </form>
              )}

              {trade.status === 'awaiting_payment' && !buyer && <div className="c2c-action-wait"><strong>等待买家付款</strong><span>截止 {formatC2CDate(trade.payment_deadline)}</span></div>}
              {trade.status === 'paid' && seller && <div className="c2c-release-actions"><Button disabled={Boolean(acting)} onClick={release}>{acting === 'release' ? '正在放行' : '确认收款并放行'}</Button><Link className="button button-secondary" to={`/c2c/trades/${trade.id}/dispute`}>发起争议</Link></div>}
              {trade.status === 'paid' && buyer && <div className="c2c-action-wait"><strong>已声明付款</strong><span>等待卖家确认到账</span><Link to={`/c2c/trades/${trade.id}/dispute`}>发起争议</Link></div>}
              {trade.status === 'disputed' && <div className="c2c-release-actions"><Link className="button button-primary" to={`/c2c/trades/${trade.id}/dispute`}>补充证据</Link></div>}
              {isC2CTradeTerminal(trade.status) && <div className="c2c-action-wait"><strong>{c2cTradeStatusLabels[trade.status]}</strong>{trade.resolved_at && <span>{formatC2CDate(trade.resolved_at)}</span>}</div>}
            </section>

            <aside className="panel c2c-trade-meta">
              <header className="panel-heading"><h2>时间</h2></header>
              <dl className="detail-list">
                <div><dt>创建</dt><dd>{formatC2CDate(trade.created_at)}</dd></div>
                <div><dt>付款截止</dt><dd>{formatC2CDate(trade.payment_deadline)}</dd></div>
                {trade.paid_at && <div><dt>声明付款</dt><dd>{formatC2CDate(trade.paid_at)}</dd></div>}
                {trade.review_due_at && <div><dt>复核期限</dt><dd>{formatC2CDate(trade.review_due_at)}</dd></div>}
              </dl>
              {trade.payment_reference && <div className="c2c-reference"><span>付款备注</span><strong>{trade.payment_reference}</strong></div>}
            </aside>
          </div>

          {(trade.statements.length > 0 || trade.evidence.length > 0) && (
            <section className="panel c2c-evidence-panel">
              <header className="panel-heading"><h2>争议与凭证</h2></header>
              <div className="c2c-evidence-grid">
                {trade.statements.map((statement) => <article key={statement.id}><header><strong>{statement.actor_display_name}</strong><span>{formatC2CDate(statement.created_at)}</span></header><p>{statement.deleted_at ? '内容已按保留期清理' : statement.text}</p></article>)}
                {trade.evidence.map((item) => <a className="c2c-evidence-file" href={item.download_url} key={item.id}><strong>{item.kind === 'payment' ? '付款截图' : '争议图片'}</strong><span>{item.deleted_at ? '已清理' : `${item.uploader_name} · ${Math.ceil(item.size_bytes / 1024)} KB`}</span></a>)}
              </div>
            </section>
          )}

          <section className="panel c2c-event-panel">
            <header className="panel-heading"><h2>交易记录</h2></header>
            <ol>{trade.events.map((event) => <li key={event.id}><span>{formatC2CDate(event.created_at)}</span><strong>{event.reason || event.action}</strong></li>)}</ol>
          </section>
        </>
      ) : null}
    </AppShell>
  )
}
