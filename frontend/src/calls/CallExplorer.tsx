import { useMemo, useState, type ReactNode } from 'react'
import type { CallDetail, CallStats } from '../api/types'
import { Button, QueryBoundary } from '../ui'
import { CallDetailDrawer } from './CallDetail'
import { CallStatsBar } from './CallStatsBar'
import { CallTable, type CallColumn, type CallRowBase } from './CallTable'
import { LiveToggle } from './LiveToggle'
import { useCall, useCallPages, type CallListParams, type CursorPage } from './queries'
import { useCallStream } from './useCallStream'

type Page<T> = CursorPage<T> & { summary?: CallStats }

/** 实时到达的一条是否符合当前筛选（时间范围由服务端流保证为最新，不再比较）。 */
export function matchesFilters(row: CallRowBase & Record<string, unknown>, params: CallListParams) {
  if (params.model && row.model_id !== params.model) return false
  if (params.outcome && row.outcome !== params.outcome) return false
  if (params.format && row.format !== params.format) return false
  if (params.tag && row.tag !== params.tag) return false
  if (params.api_key_id) {
    const key = row.api_key as { id?: string } | null | undefined
    if (key?.id !== params.api_key_id) return false
  }
  return true
}

/**
 * 调用浏览器：汇总 + 实时开关 + 列表 + 加载更多 + 详情抽屉。
 * 用户用量页、渠道编辑页与管理员调用页共用；接口路径与列由调用方决定。
 */
export function CallExplorer<T extends CallRowBase>({
  caption,
  listPath,
  streamPath,
  detailPath,
  params,
  columns,
  toolbar,
  chargedLabel,
  openCallId,
  onOpenCall,
}: {
  caption: string
  listPath: string
  /** 提供时显示「实时」开关 */
  streamPath?: string
  /** 提供时行可点击并打开详情抽屉（GET {detailPath}/{id}） */
  detailPath?: string
  params: CallListParams
  columns: CallColumn<T>[]
  toolbar?: ReactNode
  chargedLabel?: string
  /** 受控的已打开调用 ID（例如按请求 ID 搜索） */
  openCallId?: string | null
  onOpenCall?: (id: string | null) => void
}) {
  const pages = useCallPages<Page<T>>(listPath, params)
  const [live, setLive] = useState(false)
  const stream = useCallStream<T>(live && streamPath ? streamPath : null)
  const [localOpen, setLocalOpen] = useState<string | null>(null)
  const selected = openCallId !== undefined ? openCallId : localOpen
  const setSelected = onOpenCall ?? setLocalOpen
  const detail = useCall(detailPath ? selected : null, detailPath)

  const loaded = useMemo(() => pages.data?.pages.flatMap((page) => page.items) ?? [], [pages.data])
  const fresh = useMemo(
    () => stream.items.filter((row) => matchesFilters(row as T & Record<string, unknown>, params)),
    [stream.items, params],
  )
  const rows = useMemo(() => {
    const seen = new Set(fresh.map((row) => row.id))
    return [...fresh, ...loaded.filter((row) => !seen.has(row.id))]
  }, [fresh, loaded])
  const highlight = useMemo(() => new Set(fresh.map((row) => row.id)), [fresh])
  const summary = pages.data?.pages[0]?.summary

  return (
    <div className="call-explorer">
      <div className="call-explorer-bar">
        {toolbar}
        {streamPath && (
          <LiveToggle
            enabled={live}
            onChange={(value) => {
              setLive(value)
              if (!value) stream.clear()
            }}
            onRetry={stream.retry}
            status={stream.status}
          />
        )}
      </div>
      {summary && <CallStatsBar chargedLabel={chargedLabel} stats={summary} />}
      <QueryBoundary errorFallback="调用记录加载失败" query={{ ...pages, data: pages.data ? rows : undefined }}>
        {(data) => (
          <>
            <CallTable
              caption={caption}
              columns={columns}
              highlight={highlight}
              onSelect={detailPath ? (row) => setSelected(row.id) : undefined}
              rows={data}
            />
            {pages.hasNextPage && (
              <div className="table-pagination">
                <Button
                  loading={pages.isFetchingNextPage}
                  onClick={() => void pages.fetchNextPage()}
                  size="sm"
                  type="button"
                  variant="secondary"
                >
                  加载更多
                </Button>
              </div>
            )}
          </>
        )}
      </QueryBoundary>
      {detailPath && (
        <CallDetailDrawer
          onClose={() => setSelected(null)}
          open={Boolean(selected)}
          query={detail as { data: CallDetail | undefined; isPending: boolean; isError: boolean; error: unknown; refetch: () => unknown }}
        />
      )}
    </div>
  )
}
