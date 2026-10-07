import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { api, ApiError } from '../api/client'
import type { FeeRateSnapshot } from '../api/contracts'
import { Button, InlineError, LoadingState, TextField } from '../ui/FormControls'
import { formatFeeRatePercent, percentToFeeRate } from './feeRate'

const maxReasonLength = 256

export function FeeRatePanel() {
  const [snapshot, setSnapshot] = useState<FeeRateSnapshot | null>(null)
  const [percent, setPercent] = useState('')
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  const load = useCallback(async () => {
    try {
      setSnapshot(await api.adminFeeRates())
      return true
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '手续费率加载失败')
      return false
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!snapshot) return
    setError('')
    setMessage('')
    const feeRate = percentToFeeRate(percent)
    if (feeRate === null) {
      setError('费率须在 0% 到 100% 之间，最多 7 位小数')
      return
    }
    const trimmedReason = reason.trim()
    if (!trimmedReason) {
      setError('请填写调整原因')
      return
    }
    const current = snapshot.current
    if (feeRate === current.fee_rate) {
      setError('新费率与当前费率相同')
      return
    }
    if (!window.confirm(`将全局 API 手续费率从 ${formatFeeRatePercent(current.fee_rate)} 调整为 ${formatFeeRatePercent(feeRate)}？只影响之后的新调用。`)) return
    setSubmitting(true)
    try {
      const created = await api.setAdminFeeRate(current.version, feeRate, trimmedReason)
      setPercent('')
      setReason('')
      setMessage(`已生效：${formatFeeRatePercent(created.fee_rate)}（版本 ${created.version}）`)
      await load()
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'fee_rate_conflict') {
        await load()
        setError('费率已被其他管理员更新，已刷新为最新值，请确认后重试')
      } else {
        setError(caught instanceof ApiError ? caught.message : '手续费率保存失败')
      }
    }
    setSubmitting(false)
  }

  return (
    <section className="panel table-panel fee-rate-panel" aria-label="API 手续费率">
      <header className="table-toolbar">
        <h2>API 手续费率</h2>
        {snapshot && (
          <strong>
            当前 {formatFeeRatePercent(snapshot.current.fee_rate)} · 版本 {snapshot.current.version}
          </strong>
        )}
      </header>
      {!snapshot ? (
        error ? <InlineError>{error}</InlineError> : <LoadingState />
      ) : (
        <>
          <form className="stack-form" onSubmit={submit}>
            <InlineError>{error}</InlineError>
            {message && (
              <div aria-live="polite" className="success-message">
                {message}
              </div>
            )}
            <div className="field-row">
              <TextField
                inputMode="decimal"
                label="新费率（%）"
                onChange={(event) => setPercent(event.target.value)}
                placeholder={formatFeeRatePercent(snapshot.current.fee_rate).replace('%', '')}
                required
                value={percent}
              />
              <TextField
                label="调整原因"
                maxLength={maxReasonLength}
                onChange={(event) => setReason(event.target.value)}
                required
                value={reason}
              />
            </div>
            <div className="form-actions">
              <Button disabled={submitting} type="submit">
                {submitting ? '正在保存' : '设置新费率'}
              </Button>
            </div>
          </form>
          <div className="desktop-table-wrap">
            <table className="data-table">
              <thead>
                <tr><th>生效时间</th><th>版本</th><th>费率</th><th>操作者</th><th>原因</th></tr>
              </thead>
              <tbody>
                {snapshot.history.map((row) => (
                  <tr key={row.version}>
                    <td>{new Date(row.created_at).toLocaleString()}</td>
                    <td>{row.version}</td>
                    <td>{formatFeeRatePercent(row.fee_rate)}</td>
                    <td>{row.created_by?.username ?? '系统默认'}</td>
                    <td>{row.reason || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="mobile-card-list">
            {snapshot.history.map((row) => (
              <article className="mobile-data-card" key={row.version}>
                <header>
                  <div>
                    <strong>{formatFeeRatePercent(row.fee_rate)}</strong>
                    <span>版本 {row.version} · {new Date(row.created_at).toLocaleString()}</span>
                  </div>
                </header>
                <dl>
                  <div><dt>操作者</dt><dd>{row.created_by?.username ?? '系统默认'}</dd></div>
                  <div><dt>原因</dt><dd>{row.reason || '—'}</dd></div>
                </dl>
              </article>
            ))}
          </div>
        </>
      )}
    </section>
  )
}
