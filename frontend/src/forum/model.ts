import type { components } from '../api/schema.gen'

export type Board = components['schemas']['ForumBoard']
export type Topic = components['schemas']['ForumTopic']
export type Reply = components['schemas']['ForumReply']
export type Attachment = components['schemas']['ForumAttachment']
export type Kind = Topic['kind']
export type TicketStatus = NonNullable<Topic['status']>
export const ticketLabels: Record<TicketStatus, string> = {
  pending: '待处理', in_progress: '处理中', resolved: '已解决', closed: '已关闭',
}
export function pageNumber(value: string | null) {
  const page = Number(value)
  return Number.isInteger(page) && page > 0 && page <= 100000 ? page : 1
}
export function contentError(body: string, attachments: Attachment[]) {
  if (!body.trim()) return '请填写正文'
  if (new TextEncoder().encode(body).length > 262144) return '正文不能超过 256 KiB'
  if (attachments.length > 20) return '每篇内容最多 20 个附件'
  return ''
}
export function titleError(title: string) {
  return !title.trim() ? '请填写标题' : [...title.trim()].length > 200 ? '标题最多 200 字' : ''
}
export function attachmentSize(size: number) {
  return size >= 1048576 ? (size / 1048576).toFixed(1) + ' MiB' : Math.max(1, Math.ceil(size / 1024)) + ' KiB'
}
export function forumTime(value: string) {
  return new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
