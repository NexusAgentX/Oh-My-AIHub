import { InfoTip } from '../ui'
import { formatMultiplier } from './format'

/** 实际倍率最多显示 4 位小数，完整值放在 title 里。 */
export function shortMultiplier(value: string) {
  const number = Number(value)
  if (!Number.isFinite(number)) return value
  const rounded = Number(number.toFixed(4))
  return formatMultiplier(rounded === number ? value : String(rounded))
}

/**
 * 渠道倍率。美元模型开启「汇率优惠」时同时显示实际倍率（对积分价实际乘上的倍率，
 * 即设置的倍率 ÷ 汇率）与设置的倍率，其余只显示倍率。
 */
export function MultiplierText({
  multiplier,
  effective,
  fxDiscount,
}: {
  multiplier: string
  effective?: string
  fxDiscount?: boolean
}) {
  if (!fxDiscount || !effective) return <>{formatMultiplier(multiplier)}</>
  return (
    <span className="multiplier-text">
      <span>{shortMultiplier(effective)}</span>
      <small>
        <InfoTip label={`设置的倍率 ${formatMultiplier(multiplier)}`} trigger={formatMultiplier(multiplier)}>
          <span>
            {`渠道设置的倍率是 ${multiplier}，开启了汇率优惠，按「美元价 × ${multiplier}」扣积分，相当于积分价 ${shortMultiplier(effective)}（上面的实际倍率）。`}
          </span>
        </InfoTip>
      </small>
    </span>
  )
}
