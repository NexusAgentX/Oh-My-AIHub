export function Brand({ subtitle }: { subtitle?: string }) {
  return (
    <div className="brand">
      <span aria-hidden="true" className="brand-mark">
        O
      </span>
      <span className="brand-copy">
        <strong>Oh My AIHub</strong>
        {subtitle && <span>{subtitle}</span>}
      </span>
    </div>
  )
}
