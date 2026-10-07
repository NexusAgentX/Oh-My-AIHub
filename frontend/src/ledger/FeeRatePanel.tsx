import { useState, type FormEvent } from 'react'
import { ApiError } from '../api/client'
import { errorMessage } from '../api/query'
import {
  Button,
  Card,
  DataTable,
  Dialog,
  InlineError,
  QueryBoundary,
  SuccessMessage,
  TextField,
} from '../ui'
import { formatDateTime } from './opsFormat'
import { formatFeeRatePercent, percentToFeeRate } from './feeRate'
import { useFeeRatesQuery, useSetFeeRate } from './queries'

const maxReasonLength = 256

export function FeeRatePanel() {
  const query = useFeeRatesQuery()
  const setFeeRate = useSetFeeRate()
  const [percent, setPercent] = useState('')
  const [reason, setReason] = useState('')
  const [pending, setPending] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  const current = query.data?.current

  const review = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!current) return
    setError('')
    setMessage('')
    const feeRate = percentToFeeRate(percent)
    if (feeRate === null) {
      setError('费率须在 0% 到 100% 之间，最多 7 位小数')
      return
    }
    if (!reason.trim()) {
      setError('请填写调整原因')
      return
    }
    if (feeRate === current.fee_rate) {
      setError('新费率与当前费率相同')
      return
    }
    setPending(feeRate)
  }

  const confirm = async () => {
    if (!current || pending === null) return
    try {
      const created = await setFeeRate.mutateAsync({
        expectedVersion: current.version,
        feeRate: pending,
        reason: reason.trim(),
      })
      setPercent('')
      setReason('')
      setMessage(`已生效：${formatFeeRatePercent(created.fee_rate)}（版本 ${created.version}）`)
    } catch (caught) {
      setError(
        caught instanceof ApiError && caught.code === 'fee_rate_conflict'
          ? '费率已被其他管理员更新，已刷新为最新值，请确认后重试'
          : errorMessage(caught, '手续费率保存失败'),
      )
    }
    setPending(null)
  }

  return (
    <Card
      actions={
        current && (
          <strong className="num">
            当前 {formatFeeRatePercent(current.fee_rate)} · 版本 {current.version}
          </strong>
        )
      }
      aria-label="API 手续费率"
      flush
      title="API 手续费率"
    >
      <QueryBoundary errorFallback="手续费率加载失败" query={query}>
        {(snapshot) => (
          <>
            <form className="stack-form ops-fee-form" onSubmit={review}>
              <InlineError>{error}</InlineError>
              <SuccessMessage>{message}</SuccessMessage>
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
                <Button type="submit">设置新费率</Button>
              </div>
            </form>
            <DataTable
              caption="费率历史"
              columns={[
                {
                  key: 'rate',
                  header: '费率',
                  primary: true,
                  cell: (row) => <strong>{formatFeeRatePercent(row.fee_rate)}</strong>,
                },
                { key: 'version', header: '版本', numeric: true, cell: (row) => row.version },
                { key: 'time', header: '生效时间', cell: (row) => formatDateTime(row.created_at) },
                { key: 'by', header: '操作者', cell: (row) => row.created_by?.username ?? '系统默认' },
                { key: 'reason', header: '原因', cell: (row) => row.reason || '—' },
              ]}
              rowKey={(row) => String(row.version)}
              rows={snapshot.history}
            />
          </>
        )}
      </QueryBoundary>
      <Dialog
        busy={setFeeRate.isPending}
        description="只影响之后的新调用。"
        footer={
          <>
            <Button disabled={setFeeRate.isPending} onClick={() => setPending(null)} variant="secondary">
              取消
            </Button>
            <Button loading={setFeeRate.isPending} onClick={() => void confirm()}>
              确认调整
            </Button>
          </>
        }
        onClose={() => setPending(null)}
        open={pending !== null}
        title="调整全局手续费率"
      >
        {current && pending !== null && (
          <p className="num">
            {formatFeeRatePercent(current.fee_rate)} → {formatFeeRatePercent(pending)}
          </p>
        )}
      </Dialog>
    </Card>
  )
}
