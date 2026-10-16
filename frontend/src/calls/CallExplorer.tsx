import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { errorMessage } from '../api/query'
import type { CallDetail, CallStats } from '../api/types'
import { Button, InlineError, QueryBoundary } from '../ui'
import { CallAnalysis } from './CallAnalysis'
import { CallDetailDrawer } from './CallDetail'
import { CallStatsBar } from './CallStatsBar'
import { CallTable, type CallColumn, type CallRowBase } from './CallTable'
import { LiveToggle } from './LiveToggle'
import { useCall, useCallPages, type CallListParams, type CursorPage } from './queries'
import { useCallStream } from './useCallStream'

type Page<T> = CursorPage<T> & { summary?: CallStats }

/** 实时到达的一条是否符合当前筛选（时间范围由服务端流保证为最新，不再比较）。 */
export function matchesFilters(row: CallRowBase & Record<string, unknown>, params: CallListParams) {
  if (params.request_id && row.id !== params.request_id) return false
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
  pageSize,
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
  /** 有值时每页固定条数，翻页替换列表；省略时加载更多。 */
  pageSize?: number
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
  const [showAnalysis, setShowAnalysis] = useState(false)
  const [live, setLive] = useState(false)
  const [liveAt, setLiveAt] = useState(0)
  useEffect(() => {
    if (!live) return
    const timer = window.setInterval(() => setLiveAt(Date.now()), 15_000)
    return () => window.clearInterval(timer)
  }, [live])
  const [drill, setDrill] = useState<{ base: string; params: CallListParams; label: string } | null>(null)
  const baseKey = JSON.stringify(params)
  const drillParams = useMemo(() => (drill?.base === baseKey ? drill.params : {}), [drill, baseKey])
  const activeParams = useMemo(() => {
    const result = { ...params, ...drillParams }
    if (live && liveAt && params.to && !drillParams.to) {
      const offset = liveAt - Date.parse(params.to)
      result.to = new Date(liveAt).toISOString()
      if (params.from) result.from = new Date(Date.parse(params.from) + offset).toISOString()
    }
    return result
  }, [params, drillParams, live, liveAt])
  const pages = useCallPages<Page<T>>(listPath, activeParams, {
    limit: pageSize,
    refetchInterval: live && (!params.to || drillParams.to) ? 15_000 : false,
  })
  const [page, setPage] = useState(0)
  const currentPage = Math.min(page, Math.max(0, (pages.data?.pages.length ?? 1) - 1))
  const stream = useCallStream<T>(live && streamPath ? streamPath : null)
  const [localOpen, setLocalOpen] = useState<string | null>(null)
  const selected = openCallId !== undefined ? openCallId : localOpen
  const setSelected = onOpenCall ?? setLocalOpen
  const detail = useCall(detailPath ? selected : null, detailPath)

  const loaded = useMemo(() => pages.data?.pages.flatMap((page) => page.items) ?? [], [pages.data])
  const rows = loaded
  const displayed = pageSize
    ? currentPage === 0
      ? rows.slice(0, pageSize)
      : (pages.data?.pages[currentPage]?.items ?? [])
    : rows
  const nextPage = async () => {
    if (currentPage + 1 < (pages.data?.pages.length ?? 0)) setPage(currentPage + 1)
    else {
      const result = await pages.fetchNextPage()
      if (!result.isError && (result.data?.pages.length ?? 0) > currentPage + 1) setPage(currentPage + 1)
    }
  }
  const summary = pages.data?.pages[0]?.summary

  return (
    <div className="call-explorer">
      <div className="call-explorer-bar">
        {toolbar}
        {streamPath && (
          <LiveToggle
            enabled={live}
            onChange={(value) => {
              setPage(0)
              setLiveAt(Date.now())
              setLive(value)
              if (!value) stream.clear()
            }}
            onRetry={stream.retry}
            status={stream.status}
          />
        )}
      </div>
      {Object.keys(drillParams).length > 0 && (
        <div className="call-drill">
          <span>
            已下钻 · {drill?.label}
          </span>
          <Button variant="quiet" onClick={() => setDrill(null)}>
            返回全部筛选结果
          </Button>
        </div>
      )}
      {listPath === '/api/calls' && <div className="mobile-analysis-toggle"><Button variant="secondary" aria-expanded={showAnalysis} aria-controls="usage-analysis" onClick={() => setShowAnalysis(!showAnalysis)}>{showAnalysis ? '收起分析' : '展开分析'}</Button></div>}
      <div id="usage-analysis" className={listPath === '/api/calls' && !showAnalysis ? 'usage-analysis-collapsed' : undefined}>
      {(listPath === '/api/calls' || listPath === '/api/admin/calls') && (
        <CallAnalysis
          path={listPath}
          params={activeParams}
          live={live && (!params.to || Boolean(drillParams.to))}
          onDrill={(next, label) => {
            setPage(0)
            setDrill({ base: baseKey, params: { ...drillParams, ...next }, label })
          }}
        />
      )}
      {summary && <CallStatsBar chargedLabel={chargedLabel} stats={summary} />}
      </div>
      <QueryBoundary errorFallback="调用记录加载失败" query={{ ...pages, data: pages.data ? rows : undefined }}>
        {(data) => (
          <>
            <CallTable
              caption={caption}
              columns={columns}
              onSelect={detailPath ? (row) => setSelected(row.id) : undefined}
              rows={pageSize ? displayed : data}
            />
            {pageSize && pages.isFetchNextPageError && (
              <InlineError>{errorMessage(pages.error, '加载下一页失败，请重试')}</InlineError>
            )}
            {pageSize && (
              <nav aria-label="调用记录分页" className="channel-record-pages">
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={currentPage === 0 || pages.isFetchingNextPage}
                  onClick={() => setPage(currentPage - 1)}
                >
                  上一页
                </Button>
                <span className="muted num">第 {currentPage + 1} 页</span>
                <Button
                  variant="secondary"
                  size="sm"
                  loading={pages.isFetchingNextPage}
                  disabled={live || (!pages.hasNextPage && currentPage + 1 >= (pages.data?.pages.length ?? 0))}
                  onClick={() => void nextPage()}
                >
                  下一页
                </Button>
              </nav>
            )}
            {!pageSize && pages.hasNextPage && (
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
          query={
            detail as {
              data: CallDetail | undefined
              isPending: boolean
              isError: boolean
              error: unknown
              refetch: () => unknown
            }
          }
        />
      )}
    </div>
  )
}
