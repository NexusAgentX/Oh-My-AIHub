import { Link } from 'react-router-dom'
import { parseNanoPoints, formatNanoPoints } from '../money/amount'
import { Badge, QueryBoundary, type BadgeTone } from '../ui'
import { DetailList } from './components'
import { formatDateTime, formatPoints, isNegative, shortID } from './format'
import { useAdminAudit, useAdminTransaction } from './queries'
import type { AuditEntry, LedgerAccountRef, LedgerRelated, LedgerTransaction, TransactionType } from './types'

export const transactionTypeLabels: Record<TransactionType, [string, BadgeTone]> = {
  api_call: ['API 调用', 'info'],
  c2c_list: ['C2C 挂单', 'neutral'],
  c2c_release: ['C2C 放行', 'success'],
  c2c_return: ['C2C 退回', 'neutral'],
  admin_adjust: ['调账', 'accent'],
  bad_debt_writeoff: ['坏账核销', 'danger'],
}

export function TransactionTypeLabel({ type }: { type: TransactionType }) {
  const [label, tone] = transactionTypeLabels[type]
  return <Badge tone={tone}>{label}</Badge>
}

const systemLabels: Record<string, string> = {
  platform_revenue: '平台收入',
  c2c_escrow: 'C2C 托管',
  bad_debt: '坏账',
}

export function ledgerAccountLabel(ref: LedgerAccountRef) {
  if (ref.kind === 'user' && ref.account) return ref.account.display_name
  return systemLabels[ref.system_code ?? ''] ?? '系统账户'
}

/** 借贷合计（正常为 0）。 */
export function entriesTotal(transaction: Pick<LedgerTransaction, 'entries'>) {
  return formatNanoPoints(
    transaction.entries.reduce((sum, entry) => sum + parseNanoPoints(entry.amount), 0n),
  )
}

/** 交易规模：正向分录之和。 */
export function transactionVolume(transaction: Pick<LedgerTransaction, 'entries'>) {
  return formatNanoPoints(
    transaction.entries.reduce((sum, entry) => {
      const amount = parseNanoPoints(entry.amount)
      return amount > 0n ? sum + amount : sum
    }, 0n),
  )
}

const relatedLabels: Record<LedgerRelated['type'], string> = {
  call: '调用',
  c2c_order: '卖单',
  c2c_trade: 'C2C 交易',
  account: '账户',
}

export function RelatedLink({ related }: { related: LedgerRelated | null }) {
  if (!related) return <span className="muted">—</span>
  const label = `${relatedLabels[related.type]} ${shortID(related.id)}`
  if (related.type === 'call') return <Link to={`/admin/calls?call=${related.id}`}>{label}</Link>
  if (related.type === 'c2c_trade') return <Link to={`/admin/disputes/${related.id}`}>{label}</Link>
  return <span className="mono">{label}</span>
}

export const auditActionLabels: Record<string, string> = {
  'account.created': '创建账户',
  'account.updated': '修改账户',
  'account.password_reset': '重置密码',
  'account.password_changed': '修改密码',
  'ledger.adjust': '调账',
  'ledger.write_off': '坏账核销',
  'model.created': '上架模型',
  'model.updated': '修改模型',
  'settings.updated': '修改平台设置',
}

export function auditActionLabel(entry: Pick<AuditEntry, 'action'>) {
  return auditActionLabels[entry.action] ?? entry.action
}

function RecentManualOps({ accountID }: { accountID: string }) {
  const audit = useAdminAudit({ target_type: 'account', target_id: accountID, limit: 5 })
  return (
    <QueryBoundary query={audit}>
      {(rows) =>
        rows.length === 0 ? (
          <p className="muted-copy">没有人工操作</p>
        ) : (
          <ul className="plain-list">
            {rows.slice(0, 5).map((entry) => (
              <li key={entry.id}>
                <strong>{auditActionLabel(entry)}</strong>
                <span className="muted-copy">
                  {formatDateTime(entry.created_at)} · {entry.actor?.display_name ?? '系统'}
                  {entry.reason ? ` · ${entry.reason}` : ''}
                </span>
              </li>
            ))}
          </ul>
        )
      }
    </QueryBoundary>
  )
}

export function TransactionDetail({ transaction }: { transaction: LedgerTransaction }) {
  const total = entriesTotal(transaction)
  const balanced = parseNanoPoints(total) === 0n
  const user = transaction.entries.find((entry) => entry.ledger_account.kind === 'user')?.ledger_account.account
  return (
    <div className="drawer-sections">
      <DetailList
        items={[
          ['类型', <TransactionTypeLabel type={transaction.type} />],
          ['时间', formatDateTime(transaction.created_at)],
          ['关联对象', <RelatedLink related={transaction.related} />],
          ['经办人', transaction.actor?.display_name ?? '系统'],
          ['原因', transaction.reason || '—'],
          ['交易号', <span className="mono">{shortID(transaction.id)}</span>],
        ]}
      />
      <div>
        <h3 className="section-title">借贷分录</h3>
        <table className="entries-table">
          <caption className="visually-hidden">借贷分录</caption>
          <thead>
            <tr>
              <th scope="col">账户</th>
              <th className="cell-numeric" scope="col">
                变动
              </th>
              <th className="cell-numeric" scope="col">
                变动后余额
              </th>
            </tr>
          </thead>
          <tbody>
            {transaction.entries.map((entry, index) => (
              <tr key={index}>
                <td>{ledgerAccountLabel(entry.ledger_account)}</td>
                <td className={`cell-numeric ${isNegative(entry.amount) ? 'amount-negative' : 'amount-positive'}`}>
                  {formatPoints(entry.amount, true)}
                </td>
                <td className="cell-numeric">{formatPoints(entry.balance_after)}</td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr>
              <th scope="row">借贷合计</th>
              <td className="cell-numeric">
                <Badge tone={balanced ? 'success' : 'danger'}>
                  {balanced ? '✓ ' : ''}
                  {formatPoints(total)}
                </Badge>
              </td>
              <td />
            </tr>
          </tfoot>
        </table>
      </div>
      {transaction.related?.type === 'call' && (
        <p className="muted-copy">
          价格快照见 <Link to={`/admin/calls?call=${transaction.related.id}`}>调用详情</Link>
        </p>
      )}
      {user && (
        <div>
          <h3 className="section-title">{user.display_name} 最近的人工操作</h3>
          <RecentManualOps accountID={user.id} />
        </div>
      )}
    </div>
  )
}

export function TransactionDrawerBody({ transactionID }: { transactionID: string }) {
  const detail = useAdminTransaction(transactionID)
  return (
    <QueryBoundary query={detail}>{(data) => <TransactionDetail transaction={data.transaction} />}</QueryBoundary>
  )
}
