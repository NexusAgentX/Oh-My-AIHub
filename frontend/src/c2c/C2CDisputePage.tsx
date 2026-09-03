import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { C2CTrade } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState } from '../ui/FormControls'
import { C2CState } from './C2CActivityPage'
import { c2cStatusTone, c2cTradeStatusLabels, formatC2CDate } from './presentation'

export function C2CDisputePage() {
  const { tradeID = '' } = useParams()
  const navigate = useNavigate()
  const [trade, setTrade] = useState<C2CTrade | null>(null)
  const [statement, setStatement] = useState('')
  const [files, setFiles] = useState<File[]>([])
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    void api.c2cTrade(tradeID)
      .then(setTrade)
      .catch((caught) => setError(caught instanceof ApiError ? caught.message : '交易加载失败'))
      .finally(() => setLoading(false))
  }, [tradeID])

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!trade) return
    setSubmitting(true)
    setError('')
    try {
      await api.submitC2CDispute(trade.id, statement, files, trade.status === 'disputed')
      navigate(`/c2c/trades/${trade.id}`, { replace: true })
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '争议材料提交失败')
    } finally {
      setSubmitting(false)
    }
  }

  const editable = trade?.status === 'paid' || trade?.status === 'disputed'

  return (
    <AppShell>
      <header className="page-heading c2c-page-heading"><div><Link className="back-link" to={`/c2c/trades/${tradeID}`}>← 交易详情</Link><h1>{trade?.status === 'disputed' ? '补充证据' : '发起争议'}</h1></div>{trade && <C2CState label={c2cTradeStatusLabels[trade.status]} tone={c2cStatusTone(trade.status)} />}</header>
      <InlineError>{error}</InlineError>
      {loading ? <LoadingState /> : trade ? (
        <div className="c2c-dispute-layout">
          <section className="panel c2c-dispute-form-panel">
            <header className="panel-heading"><h2>争议陈述</h2></header>
            {editable ? <form className="c2c-form-section" onSubmit={submit}>
              <label className="field"><span className="field-label">情况说明</span><textarea className="input textarea-input c2c-statement-input" maxLength={2000} onChange={(event) => setStatement(event.target.value)} required value={statement} /><span className="field-message">{Array.from(statement).length} / 2000</span></label>
              <label className="field"><span className="field-label">图片证据（可选，最多 5 张）</span><input accept="image/jpeg,image/png" className="input c2c-file-input" multiple onChange={(event) => setFiles(Array.from(event.target.files ?? []).slice(0, 5))} type="file" /><span className="field-message">已选择 {files.length} 张</span></label>
              <div className="form-actions"><Link className="button button-secondary" to={`/c2c/trades/${trade.id}`}>取消</Link><Button disabled={submitting} type="submit">{submitting ? '正在提交' : trade.status === 'disputed' ? '补充证据' : '提交争议'}</Button></div>
            </form> : <div className="empty-state">当前状态不能提交争议</div>}
          </section>
          <aside className="panel c2c-dispute-history">
            <header className="panel-heading"><h2>已提交</h2></header>
            <div className="c2c-statement-list">
              {trade.statements.length === 0 && trade.evidence.length === 0 ? <div className="empty-state">暂无材料</div> : null}
              {trade.statements.map((item) => <article key={item.id}><header><strong>{item.actor_display_name}</strong><span>{formatC2CDate(item.created_at)}</span></header><p>{item.deleted_at ? '内容已按保留期清理' : item.text}</p></article>)}
              {trade.evidence.map((item) => <a className="c2c-evidence-file" href={item.download_url} key={item.id}><strong>{item.kind === 'payment' ? '付款截图' : '争议图片'}</strong><span>{item.deleted_at ? '已清理' : item.uploader_name}</span></a>)}
            </div>
          </aside>
        </div>
      ) : null}
    </AppShell>
  )
}
