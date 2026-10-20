import type { ReactNode } from 'react'
import { tokenPriceLabels, unitPriceLabels } from '../models/tokenPricing'
import { Link } from 'react-router-dom'
import { parseNanoPoints, formatNanoPoints } from '../money/amount'
import { MultiplierText } from '../money/MultiplierText'
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
  platform_revenue: '平台账户',
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
  'settings.updated': '修改平台设置',
  'settings.exchange_rate_synced': '同步美元汇率',
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

const remainderNote = /^(输入|输出|缓存写|缓存读)通用余量 (\d+) tokens（无可靠细分，按本组通用价）$/

/**
 * 计价说明：后端给每个有余量的桶各写一条“通用余量”。
 * 没有任何细分时，余量就是总用量，已在用量里显示，不再重复；有部分细分时合成一行。其余说明原样保留。
 */
export function pricingNotes(notes: readonly string[], hasBreakdown: boolean) {
  const remainders: string[] = []
  const rest = notes.filter((note) => {
    const match = remainderNote.exec(note)
    if (match) remainders.push(`${match[1]} ${Number(match[2]).toLocaleString('zh-CN')}`)
    return !match
  })
  return hasBreakdown && remainders.length > 0 ? [...rest, `其余按通用价：${remainders.join(' · ')} tokens`] : rest
}

const inferenceLabels: Record<string, string> = { message: '主模型推理', compaction: '上下文压缩', advisor_message: 'Advisor 子推理', image_generation_call: '图像子推理', main: '主模型推理' }

const thinkingModeLabels: Record<string, string> = { qwen_thinking: '已输出思考', qwen_non_thinking: '未输出思考' }

/** 调用类交易的价格快照：档位、倍率、手续费率与四个现价（积分/百万 token）。没有信息的项不显示。 */
export function PriceSnapshotView({ snapshot }: { snapshot: PriceSnapshot }) {
  if (snapshot.group_quote) return <GroupPriceSnapshot statement={snapshot.group_quote} />
  const detail = snapshot.detail
  const components = snapshot.components ?? []
  const thinking = thinkingModeLabels[detail?.thinking_mode ?? '']
  const notes = pricingNotes(detail?.notes ?? [], Object.keys(detail?.tokens ?? {}).length > 0)
  const items: Array<[string, ReactNode]> = [['价格档', components.length ? '按各次推理分别选档' : snapshot.tier?.name ?? '基准价']]
  if (detail?.inference_geo) {
    const geo = detail.inference_geo
    const legacyMultiplier = snapshot.selected_prices?.geo_multipliers?.[geo]
    items.push(['实际推理地域', !components.length && legacyMultiplier !== undefined ? `${geo} · 历史 token 倍率 ${legacyMultiplier}` : geo])
  }
  for (const [key, quantity] of Object.entries(detail?.units_nano ?? {})) {
    items.push([unitPriceLabels[key] ?? key, `${quantity / 1e9}（暂不收取工具附加费）`])
  }
  if (detail?.service_tier) items.push(['实际服务档位', detail.service_tier])
  if (detail?.requested_service_tier) items.push(['请求服务档位', detail.requested_service_tier])
  if (detail?.thinking_observed !== undefined) {
    items.push(['思考内容观察', detail.thinking_observed ? '已观察到' : '未观察到'])
  }
  if (thinking) {
    items.push([
      detail?.thinking_mode_basis === 'bailian_condition' ? '百炼计价模式' : '历史思考标签',
      detail?.thinking_mode_basis === 'bailian_condition' ? thinking : `${thinking}（来源未记录）`,
    ])
  }
  for (const [key, count] of Object.entries(detail?.tokens ?? {})) {
    items.push([
      tokenPriceLabels[key] || key,
      `${count} tokens · 原单价 ${snapshot.selected_prices?.token_prices?.[key] ?? snapshot.token_prices?.[key] ?? '继承通用价'}`,
    ])
  }
  if (notes.length > 0) {
    items.push([
      '计价说明',
      <span className="detail-lines" key="notes">
        {notes.map((note) => (
          <span key={note}>{note}</span>
        ))}
      </span>,
    ])
  }
  const usd = snapshot.currency === 'USD'
  if (usd) items.push(['计价币种', `美元，汇率 ${snapshot.usd_cny_rate ?? '—'}`])
  items.push(
    [
      '倍率',
      <MultiplierText
        effective={snapshot.effective_multiplier}
        fxDiscount={snapshot.fx_discount}
        key="multiplier"
        multiplier={snapshot.multiplier}
      />,
    ],
    ['手续费率', `${feeRateToPercent(snapshot.fee_rate_nano)}%`],
  )
  for (const [key, label] of (components.length ? [] : priceLabels)) {
    const selected = snapshot.selected_prices?.[key]
    let formula = `${selected} × ${snapshot.multiplier}`
    if (usd) {
      formula = snapshot.fx_discount
        ? `$${selected} × ${snapshot.multiplier}（汇率优惠）= ${formatPoints(snapshot.prices[key])}`
        : `$${selected} × ${snapshot.usd_cny_rate} × ${snapshot.multiplier} = ${formatPoints(snapshot.prices[key])}`
    }
    items.push([
      `${label}现价`,
      selected !== undefined
        ? formula
        : `${formatPoints(snapshot.prices[key])}（基准 ${formatPoints(snapshot.base_prices[key])}）`,
    ])
  }
  return (
    <>
      <DetailList items={items} />
      {components.map((component, index) => (
        <details key={index}>
          <summary>{component.model} · {inferenceLabels[component.kind] ?? component.kind}</summary>
          <DetailList items={[
            ['输入 token', String(component.usage.input_tokens)],
            ['输出 token', String(component.usage.output_tokens)],
            ['缓存读 token', String(component.usage.cache_read_tokens)],
            ['缓存写 token', String(component.usage.cache_write_tokens)],
          ]} />
          <PriceSnapshotView snapshot={component.price_snapshot} />
        </details>
      ))}
    </>
  )
}

const groupChargeKinds: Record<string, string> = {
  main: '主模型', message: '主推理', advisor_message: '子推理', image_generation_call: '图像子模型',
  request: '请求', successful_image: '成功图片', tool_call: '工具调用',
  tool_session: '工具会话', tool_second: '工具运行秒数', tool_query: '工具查询',
}
const groupChargeReasons: Record<string, string> = {
  missing_main_usage: '缺少可靠主模型用量', missing_reliable_unit: '缺少可靠计费单位',
  missing_group_quote: '缺少同分组开始时报价', missing_creation_receipt: '缺少可靠新建或计费周期事实',
  missing_unit_price: '缺少适用单位报价', execution_already_billed: '同一次上游执行已计价',
}
type GroupStatement = NonNullable<PriceSnapshot['group_quote']>
function groupChargeItems(line: GroupStatement['components'][number]) {
  const items: Array<[string, ReactNode]> = []
  if (line.reason) items.push(['说明', groupChargeReasons[line.reason] ?? line.reason])
  if (line.quote_id) items.push(['报价版本', `${shortID(line.quote_id)} · ${line.quote_version}`], ['模型修订', String(line.revision)])
  if (line.currency) {
    items.push(['币种', line.currency], ['渠道折扣', `${line.discount_nano / 1e9}×`])
    if (line.currency === 'USD') items.push(['美元汇率', String(line.rate_nano / 1e9)])
  }
  if (line.unit) {
    const unit = line.unit
    items.push(['用量', `${unit.quantity_nano / 1e9} ${groupChargeKinds[unit.unit] ?? unit.unit}${unit.tool ? ' · ' + unit.tool : ''}`])
    if (line.unit_price !== undefined) items.push(['单位原价', line.unit_price])
    const selectors = [unit.image_size, unit.image_quality, unit.image_background, unit.service_tier].filter(Boolean)
    if (selectors.length) items.push(['实际条件', selectors.join(' · ')])
  }
  if (line.usage) {
    const usage = line.usage
    items.push(['token 用量', `输入 ${usage.input_tokens} · 输出 ${usage.output_tokens} · 缓存读 ${usage.cache_read_tokens} · 缓存写 ${usage.cache_write_tokens}`])
  }
  if (line.selected_prices) {
    const prices = line.selected_prices
    items.push(['每百万 token 原价', `输入 ${prices.input} · 输出 ${prices.output} · 缓存读 ${prices.cache_read} · 缓存写 ${prices.cache_write}`])
    for (const [key, value] of Object.entries(prices.details)) items.push([tokenPriceLabels[key] ?? key, value])
  }
  if (line.tier_seq) items.push(['条件价顺序', String(line.tier_seq)])
  return items
}
function GroupPriceSnapshot({ statement }: { statement: GroupStatement }) {
  const items: Array<[string, ReactNode]> = [
    ['分组版本', `${shortID(statement.group_id)} · ${statement.group_revision}`],
    ['报价固定时间', formatDateTime(statement.started_at)],
    ['计价合计', formatPoints(statement.cost)],
    ['手续费', `${formatPoints(statement.fee)} · ${feeRateToPercent(statement.fee_rate_nano)}%`],
  ]
  if (statement.notes.length) items.push(['计价说明', <span className="detail-lines" key="notes">{statement.notes.map((note) => <span key={note}>{note}</span>)}</span>])
  return <>
    <DetailList items={items} />
    <p className="muted-copy">可靠分项精确合并后统一取整；分项显示值可能有取整差异。</p>
    {statement.components.map((line, index) => <details key={index} className="group-charge-line">
      <summary>{line.model_id || '未知模型'} · {groupChargeKinds[line.kind] ?? line.kind} · {line.state === 'unpriced' ? '未计价' : line.state === 'duplicate' ? '已去重' : formatPoints(line.cost)}</summary>
      <DetailList items={groupChargeItems(line)} />
    </details>)}
  </>
}

export function TransactionDetail({ transaction }: { transaction: LedgerTransaction }) {
  const total = entriesTotal(transaction)
  const balanced = parseNanoPoints(total) === 0n
  const user = transaction.entries.find((entry) => entry.ledger_account.kind === 'user')?.ledger_account.account
  return (
    <div className="drawer-sections">
      <DetailList
        items={[
          /* oxlint-disable react/jsx-key -- [标签, 值] 元组，不是渲染的子元素列表 */
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
          /* oxlint-enable react/jsx-key */
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
