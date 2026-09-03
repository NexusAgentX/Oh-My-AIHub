import { useCallback, useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { C2CResolutionAction, C2CTrade } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState, TextField } from '../ui/FormControls'
import { formatPointAmount } from '../wallet/presentation'
import { C2CState } from './C2CActivityPage'
import { c2cStatusTone, c2cTradeStatusLabels, formatC2CDate, formatC2CFiat } from './presentation'

export function AdminC2CDisputesPage() {
  const [trades, setTrades] = useState<C2CTrade[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    void api.adminC2CDisputes()
      .then(setTrades)
      .catch((caught) => setError(caught instanceof ApiError ? caught.message : '争议列表加载失败'))
      .finally(() => setLoading(false))
  }, [])

  return (
    <AppShell admin>
      <header className="page-heading"><div><h1>争议处理</h1></div><span className="count-badge">{trades.length}</span></header>
      <InlineError>{error}</InlineError>
      {loading ? <LoadingState /> : (
        <section className="panel table-panel">
          <div className="desktop-table-wrap"><table className="data-table"><thead><tr><th scope="col">交易</th><th scope="col">买家</th><th scope="col">卖家</th><th scope="col">数量</th><th scope="col">人民币</th><th scope="col">复核期限</th><th scope="col"><span className="visually-hidden">操作</span></th></tr></thead><tbody>{trades.map((trade) => <tr key={trade.id}><td><strong>#{trade.id.slice(0, 8)}</strong><small>{formatC2CDate(trade.updated_at)}</small></td><td>{trade.buyer_display_name}</td><td>{trade.seller_display_name}</td><td>{formatPointAmount(trade.quantity)}</td><td>{formatC2CFiat(trade.fiat_amount_fen)}</td><td>{formatC2CDate(trade.review_due_at)}</td><td className="table-action"><Link className="button button-primary" to={`/admin/c2c/disputes/${trade.id}`}>处理</Link></td></tr>)}</tbody></table></div>
          {trades.length === 0 ? <div className="empty-state">暂无待处理争议</div> : <div className="mobile-card-list">{trades.map((trade) => <article className="mobile-data-card" key={trade.id}><header><div><strong>{trade.buyer_display_name} / {trade.seller_display_name}</strong><span>{formatPointAmount(trade.quantity)} 积分 · {formatC2CFiat(trade.fiat_amount_fen)}</span></div><C2CState label="争议中" tone="danger" /></header><Link className="button button-primary" to={`/admin/c2c/disputes/${trade.id}`}>处理争议</Link></article>)}</div>}
        </section>
      )}
    </AppShell>
  )
}

export function AdminC2CDisputePage() {
  const { tradeID = '' } = useParams()
  const [trade, setTrade] = useState<C2CTrade | null>(null)
  const [reason, setReason] = useState('')
  const [loading, setLoading] = useState(true)
  const [acting, setActing] = useState('')
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    try { setTrade(await api.c2cTrade(tradeID)); setError('') } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '争议详情加载失败')
    } finally { setLoading(false) }
  }, [tradeID])

  useEffect(() => { void load() }, [load])

  const run = async (name: string, operation: () => Promise<unknown>) => {
    if (!reason.trim()) { setError('请填写裁决原因'); return }
    setActing(name)
    setError('')
    try { await operation(); await load() } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '管理员操作失败')
    } finally { setActing('') }
  }

  const resolve = (action: C2CResolutionAction) => {
    void run(action, () => api.resolveC2CDispute(tradeID, action, reason))
  }

  return (
    <AppShell admin>
      <header className="page-heading c2c-page-heading"><div><Link className="back-link" to="/admin/c2c/disputes">← 争议处理</Link><h1>争议详情</h1></div>{trade && <C2CState label={c2cTradeStatusLabels[trade.status]} tone={c2cStatusTone(trade.status)} />}</header>
      <InlineError>{error}</InlineError>
      {loading && !trade ? <LoadingState /> : trade ? (
        <>
          <section className="metric-grid c2c-admin-metrics" aria-label="争议交易摘要">
            <article className="metric-card"><span>数量</span><strong>{formatPointAmount(trade.quantity)}</strong><small>积分</small></article>
            <article className="metric-card metric-card-warm"><span>人民币</span><strong>{formatC2CFiat(trade.fiat_amount_fen)}</strong><small>外部支付</small></article>
            <article className="metric-card"><span>买家</span><strong>{trade.buyer_display_name}</strong><small>{trade.buyer_account_id.slice(0, 8)}</small></article>
            <article className="metric-card"><span>卖家</span><strong>{trade.seller_display_name}</strong><small>{trade.seller_account_id.slice(0, 8)}</small></article>
          </section>

          <div className="c2c-admin-dispute-layout">
            <section className="panel c2c-dispute-history">
              <header className="panel-heading"><h2>双方材料</h2></header>
              <div className="c2c-statement-list">
                {trade.statements.map((item) => <article key={item.id}><header><strong>{item.actor_display_name}</strong><span>{formatC2CDate(item.created_at)}</span></header><p>{item.deleted_at ? '内容已按保留期清理' : item.text}</p></article>)}
                {trade.evidence.map((item) => <a className="c2c-evidence-file" href={item.download_url} key={item.id}><strong>{item.kind === 'payment' ? '付款截图' : '争议图片'}</strong><span>{item.deleted_at ? '已清理' : `${item.uploader_name} · ${Math.ceil(item.size_bytes / 1024)} KB`}</span></a>)}
                {trade.statements.length === 0 && trade.evidence.length === 0 && <div className="empty-state">暂无材料</div>}
              </div>
            </section>

            <aside className="panel c2c-admin-resolution">
              <header className="panel-heading"><h2>仲裁处理</h2></header>
              <div className="c2c-form-section">
                <TextField label="处理原因" maxLength={512} onChange={(event) => setReason(event.target.value)} required value={reason} />
                <div className="c2c-admin-account-links">
                  <span>账户处置</span>
                  <div>
                    <Link className="button button-secondary" to={`/admin/accounts?query=${encodeURIComponent(trade.buyer_account_id)}`}>管理买家账户</Link>
                    <Link className="button button-secondary" to={`/admin/accounts?query=${encodeURIComponent(trade.seller_account_id)}`}>管理卖家账户</Link>
                  </div>
                </div>
                <div className="c2c-admin-actions">
                  <Button disabled={Boolean(acting)} onClick={() => resolve('release_to_buyer')}>放行给买家</Button>
                  <Button disabled={Boolean(acting)} onClick={() => resolve('return_to_seller')} variant="secondary">退还给卖家</Button>
                  <Button disabled={Boolean(acting) || trade.status !== 'disputed'} onClick={() => resolve('extend_review')} variant="secondary">延长复核</Button>
                  <Button disabled={Boolean(acting)} onClick={() => void run('cancel-order', () => api.adminCancelC2COrder(trade.order_id, reason))} variant="danger">取消剩余挂单</Button>
                </div>
              </div>
            </aside>
          </div>
        </>
      ) : null}
    </AppShell>
  )
}
