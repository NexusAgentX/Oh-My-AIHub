import { Button } from './Button'

/**
 * 光标式翻页：只前进与回到开头，不发页码。
 * 放在内容区末尾，列表不足一屏时也停在页面底部（样式见 Pagination.css）。
 */
export function Pagination({
  firstLabel = '回到第一页',
  hasPrevious,
  onPrevious,
  hasNext,
  onNext,
}: {
  /** 按数据顺序取名：时间倒序的列表用「回到最新」 */
  firstLabel?: string
  hasPrevious: boolean
  onPrevious: () => void
  hasNext: boolean
  onNext: () => void
}) {
  return (
    <nav aria-label="分页" className="pagination">
      <Button type="button" variant="quiet" disabled={!hasPrevious} onClick={onPrevious}>
        {firstLabel}
      </Button>
      <Button type="button" variant="secondary" disabled={!hasNext} onClick={onNext}>
        下一页
      </Button>
    </nav>
  )
}
