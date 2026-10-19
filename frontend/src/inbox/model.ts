import type { components } from '../api/schema.gen'
export type Inbox = components['schemas']['InboxResponse']
export type InboxSource = components['schemas']['InboxSource']
export type InboxItem = components['schemas']['InboxItem']
export type Category = InboxItem['category']
export const categoryLabels = { action: '需要我处理', anomaly: '异常', progress: '进行中' } as const
export function reminderCount(sources: InboxSource[], category?: Category): number | undefined {
  if (sources.some(source => source.state !== 'complete' || !source.counts)) return undefined
  return sources.reduce((sum, source) => sum + (category ? source.counts![category] : source.counts!.action + source.counts!.anomaly), 0)
}
export function inboxRows(sources: InboxSource[]) {
  return sources.flatMap(source => source.items.map(item => ({ ...item, sourceID: source.id, sourceLabel: source.label, workspace: source.workspace })))
    .sort((a, b) => {
      if (a.deadline && b.deadline) return new Date(a.deadline).getTime() - new Date(b.deadline).getTime()
      if (a.deadline) return -1
      if (b.deadline) return 1
      if (a.blocker !== b.blocker) return a.blocker ? -1 : 1
      return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
    })
}
export function homeActions(sources: InboxSource[]) {
  return inboxRows(sources).filter(item => item.category !== 'progress' && (item.blocker || item.category === 'action' && Boolean(item.deadline)))
}
