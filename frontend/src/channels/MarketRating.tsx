import { useId } from 'react'

/** 1～5 分评分：单选按钮组，键盘可用；value 为当前用户已给的分数。 */
export function MarketRating({
  value,
  disabled,
  onChange,
}: {
  value: number | null
  disabled?: boolean
  onChange: (score: number) => void
}) {
  const groupName = useId()
  return (
    <fieldset className="market-rating" disabled={disabled}>
      <legend>你的评分</legend>
      <div aria-label="渠道评分" role="radiogroup">
        {[1, 2, 3, 4, 5].map((score) => (
          <label className={value !== null && score <= value ? 'filled' : undefined} key={score}>
            <input
              checked={value === score}
              name={groupName}
              onChange={() => onChange(score)}
              type="radio"
              value={score}
            />
            <span aria-hidden="true">★</span>
            <span className="visually-hidden">{score} 分</span>
          </label>
        ))}
      </div>
    </fieldset>
  )
}
