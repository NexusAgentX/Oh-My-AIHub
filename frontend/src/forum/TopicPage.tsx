import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { errorMessage } from '../api/query'
import { MarkdownContent } from '../markdown'
import { Button, ButtonLink, Card, ConfirmDialog, InlineError, Notice, PageHeader, QueryBoundary, SelectField } from '../ui'
import { Attachments, ForumPagination, TicketBadge } from './components'
import { ContentComposer } from './ContentComposer'
import { forumTime, ticketLabels, type Reply, type TicketStatus, type Topic } from './model'
import { forumApi, useBoards, useForumAction, useReplies, useTopic } from './queries'

export function TopicPage() {
  const { id = '' } = useParams()
  const query = useTopic(id)
  return <QueryBoundary query={query}>{(topic) => <TopicDetail key={topic.id} topic={topic} />}</QueryBoundary>
}
function TopicDetail({ topic }: { topic: Topic }) {
  const { account } = useAuth()
  const navigate = useNavigate()
  const boards = useBoards()
  const [page, setPage] = useState(1)
  const replies = useReplies(topic.id, page)
  const createReply = useForumAction(forumApi.createReply)
  const remove = useForumAction(forumApi.deleteTopic)
  const [deleting, setDeleting] = useState(false)
  const [draft, setDraft] = useState(0)
  const canEdit = Boolean(account?.is_admin || account?.id === topic.author.id)
  const tickets = topic.kind === 'ticket'
  const back = tickets ? '/forum/tickets' : '/forum' + (topic.board_id ? '?board=' + topic.board_id : '')
  return <div className="forum-page forum-detail">
    <PageHeader title={topic.title} back={<Link to={back}>{tickets ? '返回工单' : '返回论坛'}</Link>}
      actions={canEdit && <><ButtonLink to={'/forum/topics/' + topic.id + '/edit'}>编辑</ButtonLink><Button type="button" variant="quiet" onClick={() => { remove.reset(); setDeleting(true) }}>删除</Button></>} />
    <div className="forum-meta">
      <strong>{topic.author.display_name}</strong>
      <time dateTime={topic.created_at}>{forumTime(topic.created_at)}</time>
      {tickets ? <><TicketBadge status={topic.status} /><span>仅提交者和管理员可见</span></> : <span>{boards.data?.items.find((board) => board.id === topic.board_id)?.name}</span>}
    </div>
    <Card as="article" className="forum-post"><MarkdownContent key={topic.id + '-body'} value={topic.body} /><Attachments items={topic.attachments} /></Card>
    {tickets && <TicketControls key={topic.status} topic={topic} />}
    <section aria-label="回复" className="forum-replies">
      <h2>{topic.reply_count} 条回复</h2>
      <QueryBoundary query={replies}>{(data) => <>
        {data.items.map((reply) => <ReplyCard key={reply.id} reply={reply} />)}
        <ForumPagination page={page} total={data.total} onChange={setPage} />
      </>}</QueryBoundary>
    </section>
    {topic.status === 'closed' ? <Notice tone="info">工单已关闭，重新打开后可以继续回复。</Notice> : <Card title="发表回复">
      <ContentComposer key={topic.id + '-new-reply-' + draft} submitLabel="发表回复" onSave={async (body, attachment_ids) => {
        await createReply.mutateAsync({ id: topic.id, body: { body, attachment_ids } })
        setDraft((value) => value + 1)
        setPage(Math.max(1, Math.ceil((topic.reply_count + 1) / 20)))
      }} />
    </Card>}
    <ConfirmDialog open={deleting} onClose={() => setDeleting(false)} title={tickets ? '删除工单' : '删除帖子'} description="正文、回复及附件将一起删除。此操作不可撤销。" confirmLabel="确认删除" danger busy={remove.isPending}
      error={remove.isError ? errorMessage(remove.error, '删除失败') : ''} onConfirm={() => remove.mutate(topic.id, { onSuccess: () => navigate(back, { replace: true }) })} />
  </div>
}
function TicketControls({ topic }: { topic: Topic }) {
  const { account } = useAuth()
  const update = useForumAction(forumApi.status)
  const [status, setStatus] = useState<TicketStatus>(topic.status || 'pending')
  if (!account?.is_admin && account?.id !== topic.author.id) return null
  const change = (value: TicketStatus) => update.mutate({ id: topic.id, status: value })
  return <div className="forum-ticket-controls">
    {account?.is_admin ? <>
      <SelectField label="处理状态" value={status} disabled={update.isPending} onChange={(event) => setStatus(event.target.value as TicketStatus)}>
        {Object.entries(ticketLabels).map(([value, label]) => <option value={value} key={value}>{label}</option>)}
      </SelectField>
      <Button type="button" variant="secondary" disabled={status === topic.status} loading={update.isPending} onClick={() => change(status)}>更新状态</Button>
    </> : <>
      {topic.status !== 'closed' && <Button type="button" variant="secondary" loading={update.isPending} onClick={() => change('closed')}>关闭工单</Button>}
      {(topic.status === 'closed' || topic.status === 'resolved') && <Button type="button" variant="secondary" loading={update.isPending} onClick={() => change('pending')}>重新打开</Button>}
    </>}
    <InlineError>{update.isError ? errorMessage(update.error, '状态更新失败') : ''}</InlineError>
  </div>
}
function ReplyCard({ reply }: { reply: Reply }) {
  const { account } = useAuth()
  const update = useForumAction(forumApi.updateReply)
  const remove = useForumAction(forumApi.deleteReply)
  const [editing, setEditing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const canEdit = Boolean(account?.is_admin || account?.id === reply.author.id)
  return <article className="forum-reply">
    <header><div className="forum-meta"><strong>{reply.author.display_name}</strong><time dateTime={reply.created_at}>{forumTime(reply.created_at)}</time></div>
      {canEdit && !editing && <div className="forum-inline-actions"><Button type="button" size="sm" variant="quiet" onClick={() => setEditing(true)}>编辑回复</Button><Button type="button" size="sm" variant="quiet" onClick={() => { remove.reset(); setDeleting(true) }}>删除回复</Button></div>}
    </header>
    {editing ? <ContentComposer key={reply.id + '-edit'} initialBody={reply.body} initialAttachments={reply.attachments} submitLabel="保存回复" onCancel={() => setEditing(false)}
      onSave={async (body, attachment_ids) => { await update.mutateAsync({ id: reply.id, body: { body, attachment_ids } }); setEditing(false) }} /> : <><MarkdownContent value={reply.body} /><Attachments items={reply.attachments} /></>}
    <ConfirmDialog open={deleting} onClose={() => setDeleting(false)} title="删除回复" description="回复及其附件将一起删除。此操作不可撤销。" confirmLabel="删除回复" danger busy={remove.isPending}
      error={remove.isError ? errorMessage(remove.error, '删除失败') : ''} onConfirm={() => remove.mutate(reply.id, { onSuccess: () => setDeleting(false) })} />
  </article>
}
