/** 开关（role="switch"）：启停、上架、实时等二元状态。 */
export function Switch({
  checked,
  onChange,
  label,
  disabled,
  showLabel = true,
}: {
  checked: boolean
  onChange: (checked: boolean) => void
  label: string
  disabled?: boolean
  /** false 时 label 只作为无障碍名称 */
  showLabel?: boolean
}) {
  return (
    <button
      aria-checked={checked}
      aria-label={showLabel ? undefined : label}
      className={`switch ${checked ? 'switch-on' : ''}`}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      role="switch"
      type="button"
    >
      <span aria-hidden="true" className="switch-track">
        <span className="switch-thumb" />
      </span>
      {showLabel && <span>{label}</span>}
    </button>
  )
}
