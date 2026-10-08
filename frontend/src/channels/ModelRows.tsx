import type { Format } from '../api/types'
import { formatLabels, formats } from '../calls'
import { formatPoints } from '../money/format'
import { useModelDetails, useModels } from '../models/queries'
import { currentPrices, multiplyPrice } from '../models/pricing'
import { Icon, IconButton } from '../ui'
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
            title={passed === undefined ? undefined : passed ? '测试通过' : '测试未通过'}
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
export function ModelRows({ rows, onChange }: { rows: ModelRow[]; onChange: (rows: ModelRow[]) => void }) {
  const catalog = useModels()
  const ids = [...new Set(rows.filter((row) => row.modelId).map((row) => row.modelId))]
  const details = useModelDetails(ids)
  const priceOf = (row: ModelRow) => {
    const detail = details[ids.indexOf(row.modelId)]?.data
    if (!detail) return '—'
    const prices = currentPrices(detail).prices
    const input = multiplyPrice(prices.input, row.multiplier || '1')
    const output = multiplyPrice(prices.output, row.multiplier || '1')
    return input && output ? `${formatPoints(input, { digits: 2 })} / ${formatPoints(output, { digits: 2 })}` : '—'
  }
  const update = (key: string, patch: Partial<ModelRow>) =>
    onChange(rows.map((row) => (row.key === key ? { ...row, ...patch } : row)))
  const options = catalog.data?.items.map((model) => model.id) ?? []

  if (rows.length === 0) return <p className="muted-copy">没有模型</p>
  return (
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
          <FormatToggles onChange={(next) => update(row.key, { formats: next })} row={row} />
          <input
            aria-label="倍率"
            className="input model-row-multiplier num"
            inputMode="decimal"
            onChange={(event) => update(row.key, { multiplier: event.target.value.trim() })}
            value={row.multiplier}
          />
          <span className="model-row-price num">{priceOf(row)}</span>
          <IconButton icon={<Icon name="x" />} label="移除" onClick={() => onChange(rows.filter((item) => item.key !== row.key))} />
        </li>
      ))}
    </ul>
  )
}
