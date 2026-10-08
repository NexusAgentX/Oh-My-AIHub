/** 进度条：芥末黄；warn 时（如预算 ≥80%）加深。 */
export function ProgressBar({
  value,
  label,
  warn,
}: {
  /** 0～100 */
  value: number
  label: string
  warn?: boolean
}) {
  const clamped = Math.max(0, Math.min(100, value))
  return (
    <div
      aria-label={label}
      aria-valuemax={100}
      aria-valuemin={0}
      aria-valuenow={Math.round(clamped)}
      className={`progress ${warn ? 'progress-warn' : 'progress-calm'}`}
      role="progressbar"
    >
      <i style={{ width: `${clamped}%` }} />
    </div>
  )
}
