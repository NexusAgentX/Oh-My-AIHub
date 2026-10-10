const sheet = 'M18 4H40L54 18V40A14 14 0 0 1 40 54H18A14 14 0 0 1 4 40V18A14 14 0 0 1 18 4Z'

/** 主标志：卷角贴纸，中间一个 O。颜色来自 token，随主题切换；静态版本见 public/favicon.svg。 */
export function BrandMark({ size = 28 }: { size?: number }) {
  return (
    <svg aria-hidden="true" className="brand-mark" height={size} viewBox="0 0 64 64" width={size}>
      <path className="mascot-shadow" d={sheet} transform="translate(5 5)" />
      <path className="mascot-body" d={sheet} />
      <circle className="brand-mark-o" cx="26" cy="31" r="9" />
      <path className="brand-mark-curl" d="M40 4V14A4 4 0 0 0 44 18H54Z" />
    </svg>
  )
}

/** 标志加字标：侧栏、顶栏与身份页使用。 */
export function Brand({ subtitle }: { subtitle?: string }) {
  return (
    <div className="brand">
      <BrandMark />
      <span className="brand-copy">
        <strong>Oh-My-AIHub</strong>
        {subtitle && <span>{subtitle}</span>}
      </span>
    </div>
  )
}
