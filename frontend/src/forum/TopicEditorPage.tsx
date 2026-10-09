import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { ButtonLink, EmptyState, PageHeader, QueryBoundary, SelectField, TextField } from '../ui'
import { ContentComposer } from './ContentComposer'
import { titleError, type Board, type Kind, type Topic } from './model'
import { forumApi, useBoards, useForumAction, useTopic } from './queries'

export function NewTopicPage() { return <NewTopic kind="discussion" /> }
export function NewTicketPage() { return <NewTopic kind="ticket" /> }
function NewTopic({ kind }: { kind: Kind }) {
  const boards = useBoards()
  const { account } = useAuth()
  if (kind === 'ticket') return <TopicForm key="new-ticket" kind={kind} boards={[]} />
  return <QueryBoundary query={boards}>{(data) => data.items.length ? <TopicForm key="new-discussion" kind={kind} boards={data.items} /> : <>
    <PageHeader title="发表帖子" back={<Link to="/forum">返回论坛</Link>} />
    <EmptyState title="暂时没有板块" description={account?.is_admin ? '先创建一个板块' : '请等待管理员创建板块'} action={account?.is_admin ? <ButtonLink to="/forum/boards">管理板块</ButtonLink> : undefined} />
  </>}</QueryBoundary>
}
export function EditTopicPage() {
  const { id = '' } = useParams()
  const query = useTopic(id)
  const { account } = useAuth()
  return <QueryBoundary query={query}>{(topic) => {
    if (!account?.is_admin && account?.id !== topic.author.id) return <EmptyState title="没有编辑权限" action={<ButtonLink to={'/forum/topics/' + id}>返回详情</ButtonLink>} />
    return <TopicForm key={id} kind={topic.kind} topic={topic} boards={[]} />
  }}</QueryBoundary>
}
function TopicForm({ kind, topic, boards }: { kind: Kind; topic?: Topic; boards: Board[] }) {
  const navigate = useNavigate()
  const active = useRef(true)
  useEffect(() => { active.current = true; return () => { active.current = false } }, [])
  const [params] = useSearchParams()
  const [title, setTitle] = useState(topic?.title || '')
  const [board, setBoard] = useState(boards.find((item) => item.id === params.get('board'))?.id || boards[0]?.id || '')
  const create = useForumAction(forumApi.createTopic)
  const update = useForumAction(forumApi.updateTopic)
  const back = topic ? '/forum/topics/' + topic.id : kind === 'ticket' ? '/forum/tickets' : '/forum'
  return <div className="forum-page">
    <PageHeader title={(topic ? '编辑' : kind === 'ticket' ? '提交' : '发表') + (kind === 'ticket' ? '工单' : '帖子')}
      description={kind === 'ticket' ? '仅你和管理员可见' : undefined} back={<Link to={back}>返回</Link>} />
    <ContentComposer key={topic?.id || 'new-' + kind} initialBody={topic?.body} initialAttachments={topic?.attachments} submitLabel={topic ? '保存修改' : kind === 'ticket' ? '提交工单' : '发表帖子'}
      onCancel={() => navigate(back)} validate={() => titleError(title) || (!topic && kind === 'discussion' && !board ? '请选择板块' : '')}
      onSave={async (body, attachment_ids) => {
        const saved = topic ? await update.mutateAsync({ id: topic.id, body: { title: title.trim(), body, attachment_ids } }) : await create.mutateAsync({ kind, title: title.trim(), body, attachment_ids, board_id: kind === 'discussion' ? board : null })
        if (active.current) navigate('/forum/topics/' + saved.id, { replace: true })
      }}>
      {!topic && kind === 'discussion' && <SelectField label="板块" required value={board} onChange={(event) => setBoard(event.target.value)}>{boards.map((item) => <option value={item.id} key={item.id}>{item.name}</option>)}</SelectField>}
      <TextField label="标题" required maxLength={200} value={title} onChange={(event) => setTitle(event.target.value)} />
    </ContentComposer>
  </div>
}
