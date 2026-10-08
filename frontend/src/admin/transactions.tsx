import { tokenPriceLabels } from '../models/tokenPricing'
import { Link } from 'react-router-dom'
import { parseNanoPoints, formatNanoPoints } from '../money/amount'
import { formatMultiplier } from '../money/format'
import { Badge, QueryBoundary, type BadgeTone } from '../ui'
import { DetailList } from './components'
import { feeRateToPercent, formatDateTime, formatPoints, isNegative, shortID } from './format'
import { useAdminTransaction } from './queries'
import type {
  AuditEntry,
  LedgerAccountRef,
  LedgerRelated,
  LedgerTransaction,
  PriceSnapshot,
  TransactionType,
} from './types'

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
  return formatNanoPoints(transaction.entries.reduce((sum, entry) => sum + parseNanoPoints(entry.amount), 0n))
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
  'model.deleted': '删除模型',
  'model.synced': '同步模型',
  'catalog.sync.rate_updated': '修改模型换算率',
  'catalog.sync.config_updated': '修改模型同步配置',
  'model.sync_removed': '清理未使用同步模型',
  'catalog.sync.completed': '完成模型同步',
  'settings.updated': '修改平台设置',
}

export function auditActionLabel(entry: Pick<AuditEntry, 'action'>) {
  return auditActionLabels[entry.action] ?? entry.action
}

function RecentManualOps({ actions }: { actions: AuditEntry[] }) {
  if (actions.length === 0) return <p className="muted-copy">没有人工操作</p>
  return (
    <ul className="plain-list">
      {actions.map((entry) => (
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

const priceLabels: Array<[keyof PriceSnapshot['prices'], string]> = [
  ['input', '输入'],
  ['output', '输出'],
  ['cache_read', '缓存读'],
  ['cache_write', '缓存写'],
]

/** 调用类交易的价格快照：档位、倍率、手续费率与四个现价（积分/百万 token）。 */
export function PriceSnapshotView({ snapshot }: { snapshot: PriceSnapshot }) {
  return (
    <DetailList
      items={[
        ['价格档', snapshot.tier?.name ?? '基准价'],
        ['实际服务档位', snapshot.detail?.service_tier || '未提供'],
        ['请求服务档位', snapshot.detail?.requested_service_tier || '未指定'],
        [
          '百炼思考模式',
          snapshot.detail?.thinking_mode === 'qwen_thinking'
            ? '已输出思考'
            : snapshot.detail?.thinking_mode === 'qwen_non_thinking'
              ? '未输出思考'
              : '未判定',
        ],
        ...Object.entries(snapshot.detail?.tokens ?? {}).map(([key, count]): [string, string] => [
          tokenPriceLabels[key] || key,
          `${count} tokens · 原单价 ${snapshot.selected_prices?.token_prices?.[key] ?? snapshot.token_prices?.[key] ?? '继承通用价'}`,
        ]),
        ...(snapshot.detail?.notes ?? []).map((note): [string, string] => ['计价说明', note]),
        ['倍率', formatMultiplier(snapshot.multiplier)],
        ['手续费率', `${feeRateToPercent(snapshot.fee_rate_nano)}%`],
        ...priceLabels.map(([key, label]): [string, string] => [
          `${label}现价`,
          snapshot.selected_prices
            ? `${snapshot.selected_prices[key]} × ${snapshot.multiplier}`
            : `${formatPoints(snapshot.prices[key])}（基准 ${formatPoints(snapshot.base_prices[key])}）`,
        ]),
      ]}
    />
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
          [
            '关联对象',
            <>
              <RelatedLink related={transaction.related} />
              {transaction.related_summary && <small className="muted-copy"> {transaction.related_summary}</small>}
            </>,
          ],
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
                变动前
              </th>
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
                <td className="cell-numeric">{formatPoints(entry.balance_before)}</td>
                <td className={`cell-numeric ${isNegative(entry.amount) ? 'amount-negative' : 'amount-positive'}`}>
                  {formatPoints(entry.amount, true)}
                </td>
                <td className="cell-numeric">{formatPoints(entry.balance_after)}</td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr>
              <th colSpan={2} scope="row">
                借贷合计
              </th>
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
      {transaction.price_snapshot && (
        <div>
          <h3 className="section-title">价格快照</h3>
          <PriceSnapshotView snapshot={transaction.price_snapshot} />
        </div>
      )}
      {transaction.recent_actions && (
        <div>
          <h3 className="section-title">{user ? `${user.display_name} 最近的人工操作` : '最近的人工操作'}</h3>
          <RecentManualOps actions={transaction.recent_actions} />
        </div>
      )}
    </div>
  )
}

export function TransactionDrawerBody({ transactionID }: { transactionID: string }) {
  const detail = useAdminTransaction(transactionID)
  return <QueryBoundary query={detail}>{(data) => <TransactionDetail transaction={data.transaction} />}</QueryBoundary>
}
