/**
 * 吉祥物（规则见 DESIGN.md「品牌」）：
 * - oh：惊讶脸，用于意外——空列表、加载失败、找不到。
 * - drop：芥末滴，用于开心时刻——欢迎、成功提示。
 * 颜色全部来自 token，随主题切换；纯装饰，对读屏隐藏。
 */
const outlines = {
  oh: 'M5 29A24 24 0 1 0 53 29A24 24 0 1 0 5 29Z',
  drop: 'M29 4C29 4 52 27 52 38.5A23 23 0 0 1 6 38.5C6 27 29 4 29 4Z',
}

export function Mascot({ kind, size = 56, bounce }: { kind: 'oh' | 'drop'; size?: number; bounce?: boolean }) {
  return (
    <svg
      aria-hidden="true"
      className={`mascot ${bounce ? 'mascot-bounce' : ''}`}
      height={size}
      viewBox="0 0 64 64"
      width={size}
    >
      <path className="mascot-shadow" d={outlines[kind]} transform="translate(5 5)" />
      <path className="mascot-body" d={outlines[kind]} />
      {kind === 'oh' ? (
        <>
          <ellipse className="mascot-ink" cx="20.5" cy="25" rx="3.2" ry="4" />
          <ellipse className="mascot-ink" cx="37.5" cy="25" rx="3.2" ry="4" />
          <ellipse className="mascot-ink" cx="29" cy="39" rx="5" ry="6" />
          <ellipse className="mascot-blush" cx="14.5" cy="34" rx="4" ry="2.6" />
          <ellipse className="mascot-blush" cx="43.5" cy="34" rx="4" ry="2.6" />
        </>
      ) : (
        <>
          <ellipse className="mascot-sheen" cx="19" cy="38" rx="3.4" ry="6" transform="rotate(20 19 38)" />
          <circle className="mascot-ink" cx="24" cy="31" r="2.2" />
          <circle className="mascot-ink" cx="36" cy="31" r="2.2" />
          <path className="mascot-smile" d="M25.5 38.5Q30 42.5 34.5 38.5" />
        </>
      )}
    </svg>
  )
}
