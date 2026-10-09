import { NavLink } from 'react-router-dom'
import { Badge, Button, Icon } from '../ui'
import { attachmentSize, ticketLabels, type Attachment, type Topic } from './model'
import './forum.css'

export function ForumTabs() {
  return <nav className="forum-tabs" aria-label="论坛分区">
    <NavLink to="/forum" end>交流</NavLink>
    <NavLink to="/forum/tickets">工单</NavLink>
  </nav>
}
export function TicketBadge({ status }: { status: Topic['status'] }) {
  return status && <Badge tone={status === 'resolved' ? 'success' : status === 'in_progress' ? 'accent' : 'neutral'}>{ticketLabels[status]}</Badge>
}
export function Attachments({ items, onRemove, disabled }: { items: Attachment[]; onRemove?: (id: string) => void; disabled?: boolean }) {
  if (!items.length) return null
  return <ul className="forum-attachments" aria-label="附件">
    {items.map((item) => <li key={item.id}>
      <Icon name="download" />
      <a href={item.url} download={item.name}>{item.name}</a>
      <span className="muted-copy">{attachmentSize(item.size)}</span>
      {onRemove && <Button type="button" size="sm" variant="quiet" disabled={disabled} aria-label={'移除附件 ' + item.name} onClick={() => onRemove(item.id)}>移除</Button>}
    </li>)}
  </ul>
}
export function ForumPagination({ page, total, limit = 20, onChange }: { page: number; total: number; limit?: number; onChange: (page: number) => void }) {
  const pages = Math.max(1, Math.ceil(total / limit))
  if (pages === 1 && page === 1) return null
  return <nav className="forum-pagination" aria-label="分页">
    <Button type="button" size="sm" variant="secondary" disabled={page <= 1} onClick={() => onChange(page - 1)}>上一页</Button>
    <span aria-live="polite">第 {page} / {pages} 页 · 共 {total} 条</span>
    <Button type="button" size="sm" variant="secondary" disabled={page >= pages} onClick={() => onChange(page + 1)}>下一页</Button>
  </nav>
}
