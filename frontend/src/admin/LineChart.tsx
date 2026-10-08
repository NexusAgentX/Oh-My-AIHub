import { useEffect, useRef, useState } from 'react'

export type ChartSeries = {
  key: string
  label: string
  /** null 表示当天无数据（例如没有成交时的均价），折线在此断开 */
  values: Array<number | null>
  /** CSS 颜色（使用 token 变量） */
  color: string
}

const height = 220
const padding = { top: 12, right: 12, bottom: 28, left: 64 }

/** 按容器实际宽度绘制，字号不随缩放变化；首次渲染（含 SSR）用 640。 */
function useWidth() {
  const reference = useRef<HTMLElement>(null)
  const [width, setWidth] = useState(640)
  useEffect(() => {
    const element = reference.current
    if (!element || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(([entry]) => setWidth(Math.max(240, Math.round(entry.contentRect.width))))
    observer.observe(element)
    return () => observer.disconnect()
  }, [])
  return { reference, width }
}

function niceRange(values: number[]) {
  let min = Math.min(0, ...values)
  let max = Math.max(0, ...values)
  if (min === max) max = min + 1
  const span = max - min
  min -= span * 0.05 * (min < 0 ? 1 : 0)
  max += span * 0.05
  return { min, max }
}

function compact(value: number) {
  const abs = Math.abs(value)
  if (abs >= 1e8) return `${(value / 1e8).toFixed(1)}亿`
  if (abs >= 1e4) return `${(value / 1e4).toFixed(1)}万`
  if (abs >= 100) return value.toFixed(0)
  return value.toFixed(2)
}

/** 按 null 把序列切成连续段。 */
export function segments(values: Array<number | null>) {
  const result: Array<Array<{ index: number; value: number }>> = []
  let current: Array<{ index: number; value: number }> = []
  values.forEach((value, index) => {
    if (value === null) {
      if (current.length) result.push(current)
      current = []
    } else {
      current.push({ index, value })
    }
  })
  if (current.length) result.push(current)
  return result
}

/** 极简 SVG 折线图：多条序列共享 Y 轴；附带视觉隐藏的数据表供读屏。 */
export function LineChart({
  title,
  labels,
  series,
}: {
  title: string
  /** X 轴标签（日期） */
  labels: string[]
  series: ChartSeries[]
}) {
  const { reference, width } = useWidth()
  const all = series.flatMap((item) => item.values).filter((value): value is number => value !== null)
  if (labels.length === 0 || all.length === 0) {
    return <p className="muted-copy">暂无走势数据</p>
  }
  const { min, max } = niceRange(all)
  const plotWidth = width - padding.left - padding.right
  const plotHeight = height - padding.top - padding.bottom
  const x = (index: number) =>
    padding.left + (labels.length === 1 ? plotWidth / 2 : (index / (labels.length - 1)) * plotWidth)
  const y = (value: number) => padding.top + (1 - (value - min) / (max - min)) * plotHeight
  const ticks = [max, (max + min) / 2, min]
  if (min < 0 && max > 0) ticks.splice(1, 1, 0)
  const xTicks = labels.length <= 2 ? labels.map((_, index) => index) : [0, Math.floor((labels.length - 1) / 2), labels.length - 1]

  return (
    <figure className="line-chart" ref={reference}>
      <svg aria-label={title} height={height} role="img" viewBox={`0 0 ${width} ${height}`} width={width}>
        {ticks.map((tick) => (
          <g key={tick}>
            <line className="chart-grid" x1={padding.left} x2={width - padding.right} y1={y(tick)} y2={y(tick)} />
            <text className="chart-axis" textAnchor="end" x={padding.left - 8} y={y(tick) + 4}>
              {compact(tick)}
            </text>
          </g>
        ))}
        {xTicks.map((index) => (
          <text
            className="chart-axis"
            key={index}
            textAnchor={index === 0 ? 'start' : index === labels.length - 1 ? 'end' : 'middle'}
            x={x(index)}
            y={height - 8}
          >
            {labels[index].slice(5)}
          </text>
        ))}
        {series.flatMap((item) =>
          segments(item.values).map((segment) =>
            segment.length === 1 ? (
              <circle
                cx={x(segment[0].index)}
                cy={y(segment[0].value)}
                fill={item.color}
                key={`${item.key}-${segment[0].index}`}
                r="2.5"
              />
            ) : (
              <polyline
                fill="none"
                key={`${item.key}-${segment[0].index}`}
                points={segment.map(({ index, value }) => `${x(index)},${y(value)}`).join(' ')}
                stroke={item.color}
                strokeLinejoin="round"
                strokeWidth="2"
              />
            ),
          ),
        )}
      </svg>
      <figcaption className="chart-legend">
        {series.map((item) => (
          <span key={item.key}>
            <i style={{ background: item.color }} />
            {item.label}
          </span>
        ))}
      </figcaption>
      <div className="visually-hidden">
        <table>
          <caption>{title}</caption>
          <thead>
            <tr>
              <th scope="col">日期</th>
              {series.map((item) => (
                <th key={item.key} scope="col">
                  {item.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {labels.map((label, index) => (
              <tr key={label}>
                <th scope="row">{label}</th>
                {series.map((item) => (
                  <td key={item.key}>{item.values[index] ?? '—'}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </figure>
  )
}
