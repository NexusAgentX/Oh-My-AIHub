import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { errorMessage } from '../api/query'
import { Button, ConfirmDialog, Dialog, EmptyState, InlineError, PageHeader, QueryBoundary, TextareaField, TextField } from '../ui'
import { type Board } from './model'
import { forumApi, useBoards, useForumAction } from './queries'
import './forum.css'

export function BoardsPage() {
  const { account } = useAuth()
  const boards = useBoards()
  const remove = useForumAction(forumApi.deleteBoard)
  const [editing, setEditing] = useState<Board | 'new' | null>(null)
  const [deleting, setDeleting] = useState<Board | null>(null)
  if (!account?.is_admin) return <EmptyState title="没有管理权限" />
  return <div className="forum-page">
    <PageHeader title="管理板块" back={<Link to="/forum">返回论坛</Link>} actions={<Button onClick={() => setEditing('new')}>新增板块</Button>} />
    <QueryBoundary query={boards}>{(data) => !data.items.length ? <EmptyState title="还没有板块" /> : <ul className="forum-board-list">
      {data.items.map((board) => <li key={board.id}>
        <div><Link to={'/forum?board=' + board.id}><strong>{board.name}</strong></Link><p className="muted-copy">{board.description}</p></div>
        <div className="forum-inline-actions"><Button size="sm" variant="secondary" onClick={() => setEditing(board)}>编辑</Button><Button size="sm" variant="quiet" onClick={() => { remove.reset(); setDeleting(board) }}>删除</Button></div>
      </li>)}
    </ul>}</QueryBoundary>
    {editing && <BoardDialog key={editing === 'new' ? 'new' : editing.id} board={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} />}
    <ConfirmDialog open={Boolean(deleting)} onClose={() => setDeleting(null)} title={'删除板块“' + (deleting?.name || '') + '”'} description="只能删除没有帖子的板块。此操作不可撤销。" confirmLabel="删除板块" danger busy={remove.isPending}
      error={remove.isError ? errorMessage(remove.error, '删除失败') : ''} onConfirm={() => { if (deleting) remove.mutate(deleting.id, { onSuccess: () => setDeleting(null) }) }} />
  </div>
}
function BoardDialog({ board, onClose }: { board?: Board; onClose: () => void }) {
  const create = useForumAction(forumApi.createBoard)
  const update = useForumAction(forumApi.updateBoard)
  const [name, setName] = useState(board?.name || '')
  const [description, setDescription] = useState(board?.description || '')
  const [order, setOrder] = useState(board?.sort_order || 0)
  const [error, setError] = useState('')
  const busy = create.isPending || update.isPending
  return <Dialog open title={board ? '编辑板块' : '新增板块'} onClose={onClose} busy={busy}>
    <form className="stack-form" onSubmit={(event) => {
      event.preventDefault(); setError('')
      if (!name.trim()) { setError('请填写板块名称'); return }
      if (new TextEncoder().encode(description).length > 2000) { setError('说明不能超过 2000 字节'); return }
      const body = { name: name.trim(), description, sort_order: order }
      void (board ? update.mutateAsync({ id: board.id, body }) : create.mutateAsync(body)).then(onClose).catch((cause) => setError(errorMessage(cause, '保存失败')))
    }}>
      <TextField label="板块名称" value={name} onChange={(event) => setName(event.target.value)} maxLength={80} required disabled={busy} />
      <TextareaField label="说明" value={description} onChange={(event) => setDescription(event.target.value)} rows={3} disabled={busy} />
      <TextField label="排序" type="number" min={-2147483648} max={2147483647} step={1} value={order} onChange={(event) => setOrder(Number(event.target.value))} disabled={busy} />
      <InlineError>{error}</InlineError><Button type="submit" loading={busy}>保存板块</Button>
    </form>
  </Dialog>
}
