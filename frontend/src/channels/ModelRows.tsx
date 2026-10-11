import type { Format } from '../api/types'
import { formatLabels, formats } from '../calls'
import { formatPoints } from '../money/format'
import { useModelDetails, useModels } from '../models/queries'
import { channelPrice, currentPrices, effectiveMultiplier } from '../models/pricing'
import { Button, Icon, IconButton, Notice } from '../ui'
import type { ModelRow } from './channelForm'

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
            {formatLabels[format]}
          </button>
        )
      })}
    </span>
  )
}

/** 选模型表格：卖、平台模型、上游名称、格式、倍率与按参考价格档的实际价。 */
export function ModelRows({ rows, onChange, onTest, testing, testingModel }: {
  rows: ModelRow[]
  onChange: (rows: ModelRow[]) => void
  onTest: (modelId: string) => void
  testing: boolean
  testingModel: string | null
}) {
  const catalog = useModels()
  const ids = [...new Set(rows.filter((row) => row.modelId).map((row) => row.modelId))]
  const details = useModelDetails(ids)
  const usdRate = (row: ModelRow) => {
    const model = catalog.data?.items.find((item) => item.id === row.modelId)
    return model?.price_currency === 'USD' ? model.usd_cny_rate : null
  }
  /** 实际倍率：勾选汇率优惠的美元模型为「倍率 ÷ 汇率」。 */
  const effectiveOf = (row: ModelRow) => effectiveMultiplier(row.multiplier || '1', row.fxDiscount, usdRate(row))
  const priceOf = (row: ModelRow) => {
    const detail = details[ids.indexOf(row.modelId)]?.data
    if (!detail) return '—'
    const prices = currentPrices(detail).prices
    const multiplier = row.multiplier || '1'
    const input = channelPrice(prices.input, multiplier, row.fxDiscount, usdRate(row))
    const output = channelPrice(prices.output, multiplier, row.fxDiscount, usdRate(row))
    return input && output ? `${formatPoints(input, { digits: 2 })} / ${formatPoints(output, { digits: 2 })}` : '—'
  }
  const rate = rows.map(usdRate).find(Boolean)
  const update = (key: string, patch: Partial<ModelRow>) =>
    onChange(rows.map((row) => (row.key === key ? { ...row, ...patch } : row)))
  const options = catalog.data?.items.map((model) => model.id) ?? []

  if (rows.length === 0) return <p className="muted-copy">没有模型</p>
  return (
    <>
    {rate && (
      <Notice tone="info">
        带「自动计算汇率优惠」的是美元模型，积分价 = 官方美元价 × {rate}。上游 1 元充 1 美元额度且按官方美元价计费：填上游分组倍率并勾选；上游按实际汇率充值：只填倍率，不勾选。
      </Notice>
    )}
    <ul className="model-rows" aria-label="模型">
      <li aria-hidden="true" className="model-rows-head">
        <span>卖</span>
        <span>平台模型 / 上游名称</span>
        <span>支持的格式</span>
        <span>倍率</span>
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
              className="input select-input mono"
              onChange={(event) => update(row.key, { modelId: event.target.value, sell: event.target.value ? row.sell || true : false })}
              value={row.modelId}
            >
              <option value="">不在目录</option>
              {[...new Set([...options, ...(row.modelId ? [row.modelId] : [])])].map((model) => (
                <option key={model} value={model}>
                  {model}
                </option>
              ))}
            </select>
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
            {formats.filter((format) => row.passed[format] === false).map((format) => (
              <p className="model-format-error" key={format}>{formatLabels[format]}：{row.errors?.[format] || '测试未通过'}</p>
            ))}
          </div>
          <div className="model-row-multiplier">
            <input
              aria-label="倍率"
              className="input num"
              inputMode="decimal"
              onChange={(event) => update(row.key, { multiplier: event.target.value.trim() })}
              value={row.multiplier}
            />
            {usdRate(row) && (
              <label className="model-row-fx">
                <input
                  checked={row.fxDiscount}
                  onChange={(event) => update(row.key, { fxDiscount: event.target.checked })}
                  type="checkbox"
                />
                <span>自动计算汇率优惠</span>
              </label>
            )}
            {usdRate(row) && row.fxDiscount && (
              <small className="model-row-effective num">实际倍率 {effectiveOf(row) ?? '—'}</small>
            )}
          </div>
          <span className="model-row-price num">{priceOf(row)}</span>
          <div className="model-row-actions">
            <Button aria-label={`测试 ${row.modelId || row.upstream} 的格式`} disabled={testing || !row.sell || !row.modelId} loading={testing && testingModel === row.modelId} onClick={() => onTest(row.modelId)} size="sm" type="button" variant="secondary">测试</Button>
            <IconButton icon={<Icon name="x" />} label="移除" onClick={() => onChange(rows.filter((item) => item.key !== row.key))} />
          </div>
        </li>
      ))}
    </ul>
    </>
  )
}
