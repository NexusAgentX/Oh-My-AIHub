/**
 * 轻量图表（不引入图表库）：迷你走势线与柱状图。
 * 数值由调用方转换为 number；标签与数值文本由调用方格式化。
 */
export function Sparkline({
  values,
  label,
  width = 160,
  height = 40,
}: {
  values: number[]
  label: string
  width?: number
  height?: number
}) {
  if (values.length < 2) return null
  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min || 1
  const step = width / (values.length - 1)
  const points = values
    .map((value, index) => `${(index * step).toFixed(1)},${(height - 3 - ((value - min) / span) * (height - 6)).toFixed(1)}`)
    .join(' ')
  return (
    <svg aria-label={label} className="sparkline" height={height} role="img" viewBox={`0 0 ${width} ${height}`} width={width}>
      <polyline fill="none" points={points} stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.8" />
    </svg>
  )
}

export type BarDatum = { key: string; label: string; value: number; display: string }

/** 柱状图：横向（适合分类）或纵向（适合按天）。 */
export function BarChart({
  data,
  label,
  orientation = 'vertical',
}: {
  data: BarDatum[]
  label: string
  orientation?: 'vertical' | 'horizontal'
}) {
  const max = Math.max(0, ...data.map((item) => item.value)) || 1
  return (
    <figure aria-label={label} className={`bar-chart bar-chart-${orientation}`} role="group">
      {data.map((item) => {
        const percent = Math.max(0, (item.value / max) * 100)
        return (
          <div className="bar-chart-item" key={item.key} title={`${item.label}：${item.display}`}>
            {orientation === 'horizontal' ? (
              <>
                <span className="bar-chart-label">{item.label}</span>
                <span className="bar-chart-track">
                  <i style={{ width: `${percent}%` }} />
                </span>
                <span className="bar-chart-value num">{item.display}</span>
              </>
            ) : (
              <>
                <span className="bar-chart-track">
                  <i style={{ height: `${percent}%` }} />
                </span>
                <span className="bar-chart-label">{item.label}</span>
              </>
            )}
            <span className="visually-hidden">{`${item.label}：${item.display}`}</span>
          </div>
        )
      })}
    </figure>
  )
}
