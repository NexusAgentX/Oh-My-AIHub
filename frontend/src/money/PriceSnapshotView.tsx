import type { ReactNode } from 'react'
import type { components } from '../api/schema.gen'
import { tokenPriceLabels, unitPriceLabels } from '../models/tokenPricing'
import { Facts } from '../ui/Facts'
import { MultiplierText } from './MultiplierText'
import { feeRateToPercent, formatExactPoints as formatPoints, shortID } from './format'
import { formatDateTime } from './time'
import './PriceSnapshotView.css'
type PriceSnapshot = components['schemas']['PriceSnapshot']

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
      <span className="price-snapshot-notes" key="notes">
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
      <Facts items={items} />
      {components.map((component, index) => (
        <details key={index}>
          <summary>{component.model} · {inferenceLabels[component.kind] ?? component.kind}</summary>
          <Facts items={[
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
  if (statement.notes.length) items.push(['计价说明', <span className="price-snapshot-notes" key="notes">{statement.notes.map((note) => <span key={note}>{note}</span>)}</span>])
  return <>
    <Facts items={items} />
    <p className="muted-copy">可靠分项精确合并后统一取整；分项显示值可能有取整差异。</p>
    {statement.components.map((line, index) => <details key={index} className="group-charge-line">
      <summary>{line.model_id || '未知模型'} · {groupChargeKinds[line.kind] ?? line.kind} · {line.state === 'unpriced' ? '未计价' : line.state === 'duplicate' ? '已去重' : formatPoints(line.cost)}</summary>
      <Facts items={groupChargeItems(line)} />
    </details>)}
  </>
}
