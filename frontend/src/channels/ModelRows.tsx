import { useEffect, useRef } from 'react'
import type { Format } from '../api/types'
import { formatLabels, formats } from '../calls'
import { formatPoints } from '../money/format'
import { shortMultiplier } from '../money/MultiplierText'
import { useModelDetails, useModels } from '../models/queries'
import { channelPrice, currentPrices, effectiveMultiplier } from '../models/pricing'
import { Button, Icon, IconButton, InfoTip } from '../ui'
import { customNameFrom, type CustomPriceForm, type ModelRow } from './channelForm'

/** 平台模型下拉中「自定义模型」的取值。 */
const CUSTOM = '__custom__'

const customPriceFields: Array<[keyof CustomPriceForm, string]> = [
  ['input', '输入'],
  ['output', '输出'],
  ['cacheWrite', '缓存写'],
  ['cacheRead', '缓存读'],
]

/** 自定义模型的四个基准价，积分/百万 token，按此直接计费。 */
function CustomPrices({ row, onChange }: { row: ModelRow; onChange: (prices: CustomPriceForm) => void }) {
  return (
    <span aria-label="自定义价格（积分/百万 token）" className="custom-prices" role="group">
      {customPriceFields.map(([field, label]) => (
        <label key={field}>
          <span>{label}</span>
          <input
            aria-label={`${label}价`}
            className="input num"
            inputMode="decimal"
            onChange={(event) => onChange({ ...row.customPrices, [field]: event.target.value.trim() })}
            value={row.customPrices[field]}
          />
        </label>
      ))}
    </span>
  )
}

/** 四个可切换的格式标签；测试通过的显示绿色 ✓。 */
function FormatToggles({ row, onChange }: { row: ModelRow; onChange: (formats: Format[]) => void }) {
  return (
    <span aria-label="支持的格式" className="format-toggles" role="group">
      {formats.map((format) => {
        const on = row.formats.includes(format)
        const passed = row.passed[format]
        return (
          <button
            aria-pressed={on}
            className={`format-toggle ${on ? 'format-toggle-on' : ''} ${passed ? 'format-toggle-ok' : ''} ${passed === false ? 'format-toggle-failed' : ''}`}
            key={format}
            onClick={() => onChange(on ? row.formats.filter((item) => item !== format) : [...row.formats, format])}
            title={passed === undefined ? undefined : passed ? '测试通过' : row.errors?.[format] || '测试未通过'}
            type="button"
          >
            {passed && <Icon name="check" size={12} />}
            {formatLabels[format]}{passed === false && ' · 失败'}
          </button>
        )
      })}
    </span>
  )
}

/** 「汇率优惠」的说明：什么时候该开、开了怎么算。 */
function FXDiscountTip({ rate }: { rate: string }) {
  return (
    <InfoTip label="什么是汇率优惠">
      <span>
        <strong>问：什么时候开启？</strong>
        <br />
        答：上游 1 元能充 1 美元额度时开启，倍率直接填上游的分组倍率（如 0.13），按「美元价 × 倍率」扣积分。
      </span>
      <span>
        <strong>问：什么时候不开启？</strong>
        <br />
        答：上游按实际汇率充值（约 {rate} 元充 1 美元）时不开启，倍率同样填上游的分组倍率。
      </span>
      <span>
        <strong>问：有什么前提？</strong>
        <br />
        答：上游要按官方美元价计费。
      </span>
    </InfoTip>
  )
}

/** 选模型表格：卖、平台模型、上游名称、格式、倍率与按参考价格档的实际价。 */
export function ModelRows({ addedRowKey, rows, onChange, onTest, testing, testingModel }: {
  addedRowKey?: string
  rows: ModelRow[]
  onChange: (rows: ModelRow[]) => void
  onTest: (modelId: string) => void
  testing: boolean
  testingModel: string | null
}) {
  const addedModel = useRef<HTMLSelectElement>(null)
  const addedCustomName = useRef<HTMLInputElement>(null)
  useEffect(() => {
    const field = addedCustomName.current ?? addedModel.current
    if (!field) return
    field.focus({ preventScroll: true })
    field.closest('.model-row')?.scrollIntoView({
      block: 'center',
      behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth',
    })
  }, [addedRowKey])
  const catalog = useModels()
  const ids = [...new Set(rows.filter((row) => row.modelId && !row.custom).map((row) => row.modelId))]
  const details = useModelDetails(ids)
  const usdRate = (row: ModelRow) => {
    if (row.custom) return null
    const model = catalog.data?.items.find((item) => item.id === row.modelId && !item.custom)
    return model?.price_currency === 'USD' ? model.usd_cny_rate : null
  }
  /** 实际倍率：勾选汇率优惠的美元模型为「倍率 ÷ 汇率」。 */
  const effectiveOf = (row: ModelRow) => effectiveMultiplier(row.multiplier || '1', row.fxDiscount, usdRate(row))
  const priceOf = (row: ModelRow) => {
    if (row.custom) {
      const { input, output } = row.customPrices
      return input && output ? `${formatPoints(input, { digits: 9 })} / ${formatPoints(output, { digits: 9 })}` : '—'
    }
    const detail = details[ids.indexOf(row.modelId)]?.data
    if (!detail) return '—'
    const prices = currentPrices(detail).prices
    const multiplier = row.multiplier || '1'
    const input = channelPrice(prices.input, multiplier, row.fxDiscount, usdRate(row))
    const output = channelPrice(prices.output, multiplier, row.fxDiscount, usdRate(row))
    return input && output ? `${formatPoints(input, { digits: 9 })} / ${formatPoints(output, { digits: 9 })}` : '—'
  }
  const update = (key: string, patch: Partial<ModelRow>) =>
    onChange(rows.map((row) => (row.key === key ? { ...row, ...patch } : row)))
  const options = catalog.data?.items.filter((model) => !model.custom).map((model) => model.id) ?? []
  const choose = (row: ModelRow, value: string) => {
    if (value === CUSTOM) {
      update(row.key, { custom: true, modelId: row.custom ? row.modelId : customNameFrom(row.upstream), sell: true, pending: false })
      return
    }
    update(row.key, { custom: false, modelId: value, sell: value ? row.sell || true : false })
  }

  if (rows.length === 0) return <p className="muted-copy">没有模型</p>
  return (
    <ul className="model-rows" aria-label="模型">
      <li aria-hidden="true" className="model-rows-head">
        <span>卖</span>
        <span>平台模型 / 上游名称</span>
        <span>支持的格式</span>
        <span>倍率 / 自定义价格</span>
        <span>实际价 入/出</span>
        <span />
      </li>
      {rows.map((row) => (
        <li className={`model-row ${row.sell ? '' : 'model-row-off'}`} key={row.key}>
          <label className="model-row-sell">
            <input
              aria-label={`卖 ${row.modelId || row.upstream}`}
              checked={row.sell}
              onChange={(event) => update(row.key, { sell: event.target.checked })}
              type="checkbox"
            />
          </label>
          <div className="model-row-names">
            <select
              aria-label="平台模型"
              ref={row.key === addedRowKey && !row.custom ? addedModel : undefined}
              className="input select-input mono"
              onChange={(event) => choose(row, event.target.value)}
              value={row.custom ? CUSTOM : row.modelId}
            >
              <option value="">不在目录</option>
              <option value={CUSTOM}>自定义模型</option>
              {[...new Set([...options, ...(row.modelId && !row.custom ? [row.modelId] : [])])].map((model) => (
                <option key={model} value={model}>
                  {model}
                </option>
              ))}
            </select>
            {row.custom && (
              <input
                aria-label="自定义模型名"
                ref={row.key === addedRowKey ? addedCustomName : undefined}
                className="input mono"
                onChange={(event) => update(row.key, { modelId: event.target.value.trim() })}
                placeholder="模型名（客户端请求的 model）"
                value={row.modelId}
              />
            )}
            <input
              aria-label="上游名称"
              className="input mono"
              onChange={(event) => update(row.key, { upstream: event.target.value })}
              placeholder="上游名称"
              value={row.upstream}
            />
          </div>
          <div className="model-row-formats">
            <FormatToggles onChange={(next) => update(row.key, { formats: next })} row={row} />
            {formats.some((format) => row.passed[format] === false) && <details className="format-test-errors">
              <summary>查看测试失败原因</summary>
              <ul>{formats.filter((format) => row.passed[format] === false).map((format) => <li key={format}><strong>{formatLabels[format]}</strong>：{row.errors?.[format] || '测试未通过'}</li>)}</ul>
            </details>}
          </div>
          <div className="model-row-multiplier">
            {row.custom ? (
              <CustomPrices onChange={(customPrices) => update(row.key, { customPrices })} row={row} />
            ) : (
              <>
                {row.pending && <p className="model-row-pending">已加入模型目录，设置倍率并勾选后恢复</p>}
                <span className="multiplier-input">
                  <span aria-hidden="true">×</span>
                  <input
                    aria-label="倍率"
                    className="input num"
                    inputMode="decimal"
                    onChange={(event) => update(row.key, { multiplier: event.target.value.trim() })}
                    value={row.multiplier}
                  />
                </span>
                {usdRate(row) && (
                  <span className="model-row-fx">
                    <button
                      aria-pressed={row.fxDiscount}
                      className={`format-toggle ${row.fxDiscount ? 'format-toggle-on' : ''}`}
                      onClick={() => update(row.key, { fxDiscount: !row.fxDiscount })}
                      title="自动计算汇率优惠"
                      type="button"
                    >
                      {row.fxDiscount && <Icon name="check" size={12} />}
                      汇率优惠
                    </button>
                    <FXDiscountTip rate={usdRate(row)!} />
                  </span>
                )}
              </>
            )}
          </div>
          <span className="model-row-price num">
            {priceOf(row)}
            {usdRate(row) && row.fxDiscount && <small title={effectiveOf(row) ?? undefined}>实际倍率 {shortMultiplier(effectiveOf(row) ?? '—')}</small>}
          </span>
          <div className="model-row-actions">
            <Button aria-label={`测试 ${row.modelId || row.upstream} 的格式`} disabled={testing || !row.sell || !row.modelId} loading={testing && testingModel === row.modelId} onClick={() => onTest(row.modelId)} size="sm" type="button" variant="secondary">测试</Button>
            <IconButton icon={<Icon name="x" />} label="移除" onClick={() => onChange(rows.filter((item) => item.key !== row.key))} />
          </div>
        </li>
      ))}
    </ul>
  )
}
