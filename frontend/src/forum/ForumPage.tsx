import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { errorMessage } from '../api/query'
import { Button, ButtonLink, EmptyState, InlineError, PageHeader, QueryBoundary, SearchInput, SelectField } from '../ui'
import { ForumPagination, ForumTabs, TicketBadge } from './components'
import { forumTime, pageNumber, type Kind } from './model'
import { useBoards, useTopics } from './queries'

export function ForumPage() { return <TopicList kind="discussion" /> }
export function TicketsPage() { return <TopicList kind="ticket" /> }
function TopicList({ kind }: { kind: Kind }) {
  const { account } = useAuth()
  const [params, setParams] = useSearchParams()
  const page = pageNumber(params.get('page'))
  const query = params.get('q') || ''
  const boardID = kind === 'discussion' ? params.get('board') || '' : ''
  const boards = useBoards()
  const topics = useTopics(kind, page, query, boardID)
  const tickets = kind === 'ticket'
  const setFilter = (key: string, value: string) => {
    const next = new URLSearchParams(params)
    next.delete('page')
    if (value) next.set(key, value); else next.delete(key)
    setParams(next)
  }
  const base = tickets ? '/forum/tickets' : '/forum'
  return <div className="forum-page">
    <PageHeader title={tickets ? account?.is_admin ? '全部工单' : '我的工单' : '论坛'}
      description={tickets ? '仅提交者和管理员可见' : undefined}
      actions={<ButtonLink to={base + '/new' + (boardID ? '?board=' + boardID : '')} variant="primary">{tickets ? '提交工单' : '发表帖子'}</ButtonLink>} />
    <ForumTabs />
    <div className="forum-filters">
      <ForumSearch key={query} query={query} onSearch={(value) => setFilter('q', value)} />
      {!tickets && <SelectField label="板块" value={boardID} onChange={(event) => setFilter('board', event.target.value)}>
        <option value="">全部板块</option>
        {boards.data?.items.map((board) => <option key={board.id} value={board.id}>{board.name}</option>)}
      </SelectField>}
      {!tickets && account?.is_admin && <ButtonLink to="/forum/boards">管理板块</ButtonLink>}
    </div>
    {!tickets && boards.isError && <InlineError>{errorMessage(boards.error, '板块加载失败')}</InlineError>}
    {boardID && <p className="muted-copy">{boards.data?.items.find((board) => board.id === boardID)?.description}</p>}
    <QueryBoundary query={topics} errorFallback="帖子加载失败">
      {(data) => <>
        {data.items.length === 0 ? <EmptyState title={query || boardID ? '没有匹配的内容' : tickets ? '还没有工单' : '还没有帖子'}
          action={page > 1 ? <Button onClick={() => setFilter('page', '')}>返回第一页</Button> : !tickets && boards.data?.items.length === 0 && account?.is_admin ? <ButtonLink to="/forum/boards">创建第一个板块</ButtonLink> : undefined} /> :
          <ul className="forum-topic-list">{data.items.map((topic) => <li key={topic.id}>
            <div className="forum-topic-main">
              <Link className="forum-topic-title" to={'/forum/topics/' + topic.id}>{topic.title}</Link>
              <div className="forum-meta">
                {!tickets && <span>{boards.data?.items.find((board) => board.id === topic.board_id)?.name || '板块'}</span>}
                <span>{topic.author.display_name}</span><time dateTime={topic.updated_at}>{forumTime(topic.updated_at)}</time>
                <span>{topic.reply_count} 条回复</span>
              </div>
            </div>
            <TicketBadge status={topic.status} />
          </li>)}</ul>}
        <ForumPagination page={page} total={data.total} onChange={(nextPage) => { const next = new URLSearchParams(params); next.set('page', String(nextPage)); setParams(next) }} />
      </>}
    </QueryBoundary>
  </div>
}
function ForumSearch({ query, onSearch }: { query: string; onSearch: (value: string) => void }) {
  const [value, setValue] = useState(query)
  return <form className="forum-search" onSubmit={(event) => { event.preventDefault(); onSearch(value.trim()) }}>
    <SearchInput label="搜索标题或正文" placeholder="搜索标题或正文" value={value} maxLength={60} onChange={(event) => setValue(event.target.value)} />
    <Button type="submit" variant="secondary">搜索</Button>
    {query && <Button type="button" variant="quiet" onClick={() => onSearch('')}>清除</Button>}
  </form>
}
