import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { withQuery } from '../api/client'
import { errorMessage } from '../api/query'
import { usePageVisible } from '../api/visibility'
import type { CallDetail, CallStats } from '../api/types'
import { Button, InlineError, Notice, QueryBoundary } from '../ui'
import { CallDetailDrawer } from './CallDetail'
import { CallStatsBar } from './CallStatsBar'
import { CallTable, type CallColumn, type CallRowBase } from './CallTable'
import { LiveToggle } from './LiveToggle'
import { useCall, useCallPages, type CallListParams, type CursorPage } from './queries'
import { useCallStream } from './useCallStream'

type Page<T> = CursorPage<T> & { summary?: CallStats }
const liveFields = new Set(['request_id', 'model', 'outcome', 'format', 'tag', 'api_key_id', 'channel_id', 'account_id', 'stream', 'from', 'to'])
export function canMergeLive(params: CallListParams) {
  return Object.entries(params).every(([key, value]) => value === undefined || liveFields.has(key))
}

/** 只判断实时 DTO 中有完整事实的条件；其余条件通过列表补拉执行。 */
export function matchesFilters(row: CallRowBase & Record<string, unknown>, params: CallListParams) {
  if (params.request_id && row.id !== params.request_id) return false
  if (params.model && row.model_id !== params.model) return false
  if (params.outcome && row.outcome !== params.outcome) return false
  if (params.format && row.format !== params.format) return false
  if (params.tag && row.tag !== params.tag) return false
  if (params.stream !== undefined && row.stream !== params.stream) return false
  if (params.from && Date.parse(row.created_at) < Date.parse(params.from)) return false
  if (params.to && Date.parse(row.created_at) >= Date.parse(params.to)) return false
  for (const [filter, field] of [['api_key_id', 'api_key'], ['channel_id', 'channel'], ['account_id', 'account']] as const) {
    if (params[filter] && (row[field] as { id?: string } | undefined)?.id !== params[filter]) return false
  }
  return true
}

/** 列表与轻量汇总；分析由单独页面按需加载。 */
export function CallExplorer<T extends CallRowBase>({
  pageSize, caption, listPath, streamPath, detailPath, params, columns, toolbar, chargedLabel,
  openCallId, onOpenCall, liveDisabledReason,
}: {
  pageSize?: number
  caption: string
  listPath: string
  streamPath?: string
  detailPath?: string
  params: CallListParams
  columns: CallColumn<T>[]
  toolbar?: ReactNode
  chargedLabel?: string
  openCallId?: string | null
  onOpenCall?: (id: string | null) => void
  liveDisabledReason?: string
}) {
  const [live, setLive] = useState(false)
  const visible = usePageVisible()
  const activeLive = live && !liveDisabledReason
  const pages = useCallPages<Page<T>>(listPath, params, { limit: pageSize, refetchInterval: activeLive ? 15_000 : false })
  const { refetch } = pages
  const [page, setPage] = useState(0)
  const [localOpen, setLocalOpen] = useState<string | null>(null)
  const selected = openCallId !== undefined ? openCallId : localOpen
  const setSelected = onOpenCall ?? setLocalOpen
  const detail = useCall(detailPath ? selected : null, detailPath)
  const root = useRef<HTMLDivElement>(null)
  const [reading, setReading] = useState(false)
  useEffect(() => {
    const check = () => setReading(window.scrollY >= 100 || Boolean(document.activeElement?.closest('.data-table, .mobile-card-list')))
    window.addEventListener('scroll', check, { passive: true })
    document.addEventListener('focusin', check)
    document.addEventListener('focusout', check)
    check()
    return () => { window.removeEventListener('scroll', check); document.removeEventListener('focusin', check); document.removeEventListener('focusout', check) }
  }, [])
  const mergeable = canMergeLive(params)
  const liveURL = streamPath ? withQuery(streamPath.split('?')[0], {
    ...Object.fromEntries(new URLSearchParams(streamPath.split('?')[1])),
    model: params.model, api_key_id: params.api_key_id, channel_id: params.channel_id, outcome: params.outcome,
  }) : null
  const stream = useCallStream<T>(activeLive ? liveURL : null)
  const fetched = useMemo(() => pages.data?.pages.flatMap(p => p.items) ?? [], [pages.data])
  const filterKey = JSON.stringify(params)
  const [base, setBase] = useState<{ key: string; rows: T[] }>({ key: '', rows: [] })
  const freeze = activeLive && (reading || Boolean(selected))
  const loaded = freeze && base.key === filterKey ? base.rows : fetched
  useEffect(() => {
    if (!freeze || base.key !== filterKey) setBase({ key: filterKey, rows: fetched })
  }, [freeze, base.key, filterKey, fetched])
  const loadedIDs = useMemo(() => new Set(loaded.map(row => row.id)), [loaded])
  const [shown, setShown] = useState<ReadonlySet<string>>(new Set())
  const seen = useRef(new Set<string>())
  useEffect(() => { setPage(0); setShown(new Set()); seen.current.clear() }, [filterKey, activeLive])
  useEffect(() => {
    const fresh = stream.items.filter(item => !seen.current.has(item.id))
    for (const item of fresh) seen.current.add(item.id)
    if (seen.current.size > 1000) seen.current = new Set(stream.items.map(item => item.id))
    if (fresh.length && mergeable && !selected && !reading) {
      setShown(current => new Set([...current, ...fresh.map(item => item.id)].slice(-200)))
    }
  }, [stream.items, mergeable, selected, reading])
  const previousVisibility = useRef(visible)
  const previousStatus = useRef(stream.status)
  useEffect(() => {
    if (activeLive && visible && (!previousVisibility.current || (stream.status === 'open' && previousStatus.current !== 'open'))) void refetch()
    previousVisibility.current = visible
    previousStatus.current = stream.status
  }, [activeLive, visible, stream.status, refetch])
  const matches = (row: T) => matchesFilters(row as T & Record<string, unknown>, params)
  const rows = useMemo(() => {
    if (!activeLive || !mergeable || pages.isPlaceholderData) return loaded
    const updates = new Map(stream.items.map(row => [row.id, row]))
    const matches = (row: T) => matchesFilters(row as T & Record<string, unknown>, params)
    const existing = loaded.map(row => updates.get(row.id) ?? row).filter(matches)
    const inserted = stream.items.filter(row => !loadedIDs.has(row.id) && shown.has(row.id) && matches(row))
    return [...inserted, ...existing].slice(0, 500)
  }, [loaded, loadedIDs, stream.items, shown, params, activeLive, mergeable, pages.isPlaceholderData])
  const pending = new Set([
    ...fetched.filter(row => !loadedIDs.has(row.id) && !shown.has(row.id)).map(row => row.id),
    ...stream.items.filter(row => !loadedIDs.has(row.id) && !shown.has(row.id) && (!mergeable || matches(row))).map(row => row.id),
  ]).size
  const currentPage = Math.min(page, Math.max(0, (pages.data?.pages.length ?? 1) - 1))
  const displayed = pageSize ? currentPage === 0 ? rows.slice(0, pageSize) : pages.data?.pages[currentPage]?.items ?? [] : rows
  const capped = (pages.data?.pages.length ?? 0) >= 10
  const nextPage = async () => {
    if (currentPage + 1 < (pages.data?.pages.length ?? 0)) setPage(currentPage + 1)
    else {
      const result = await pages.fetchNextPage()
      if (!result.isError && (result.data?.pages.length ?? 0) > currentPage + 1) setPage(currentPage + 1)
    }
  }
  return <div className="call-explorer" ref={root}>
    <div className="call-explorer-bar">
      {toolbar}
      <Button variant="quiet" size="sm" loading={pages.isFetching && !pages.isFetchingNextPage} onClick={() => void pages.refetch()}>刷新</Button>
      {streamPath && <LiveToggle enabled={activeLive} onChange={value => { setLive(value); setPage(0); stream.clear() }} status={stream.status} onRetry={stream.retry} disabledReason={liveDisabledReason} />}
    </div>
    {pages.data?.pages[0]?.summary && <CallStatsBar chargedLabel={chargedLabel} stats={pages.data.pages[0].summary} />}
    {activeLive && pending > 0 && <Notice tone="info" action={<Button size="sm" variant="quiet" onClick={() => {
      if (mergeable) { setBase({ key: filterKey, rows: fetched }); setShown(new Set(stream.items.map(item => item.id))); window.scrollTo({ top: 0 }) }
      else { void pages.refetch(); stream.clear() }
    }}>{mergeable ? '显示' : '刷新'}</Button>}>{mergeable ? `${pending} 条新调用` : '调用有更新'}</Notice>}
    {pages.isRefetchError && pages.data && <InlineError>刷新失败，正在显示上次结果。<Button size="sm" variant="quiet" onClick={() => void pages.refetch()}>重试</Button></InlineError>}
    <div aria-busy={pages.isFetching} className={pages.isPlaceholderData ? 'query-updating' : undefined}>
      <QueryBoundary errorFallback="调用记录加载失败" query={{ ...pages, data: pages.data ? rows : undefined }}>{() => <>
        <CallTable caption={caption} columns={columns} rows={displayed} highlight={activeLive ? shown : undefined} onSelect={detailPath ? row => setSelected(row.id) : undefined} />
        {pages.isFetchNextPageError && <InlineError>{errorMessage(pages.error, '加载下一页失败，请重试')}</InlineError>}
        {pageSize ? <nav className="channel-record-pages" aria-label="调用记录分页">
          <Button size="sm" variant="secondary" disabled={currentPage === 0 || pages.isFetchingNextPage} onClick={() => setPage(currentPage - 1)}>上一页</Button>
          <span className="num">第 {currentPage + 1} 页</span>
          <Button size="sm" variant="secondary" loading={pages.isFetchingNextPage} disabled={activeLive || (!pages.hasNextPage && currentPage + 1 >= (pages.data?.pages.length ?? 0)) || (capped && currentPage + 1 >= (pages.data?.pages.length ?? 0))} onClick={() => void nextPage()}>下一页</Button>
        </nav> : pages.hasNextPage && !capped && <div className="table-pagination"><Button variant="secondary" size="sm" loading={pages.isFetchingNextPage} onClick={() => void pages.fetchNextPage()}>加载更多</Button></div>}
        {capped && pages.hasNextPage && <p className="muted-copy">已显示 10 页，请缩小筛选范围。</p>}
      </>}</QueryBoundary>
    </div>
    {detailPath && <CallDetailDrawer onClose={() => setSelected(null)} open={Boolean(selected)} query={detail as { data: CallDetail | undefined; isPending: boolean; isError: boolean; error: unknown; refetch: () => unknown }} />}
  </div>
}
