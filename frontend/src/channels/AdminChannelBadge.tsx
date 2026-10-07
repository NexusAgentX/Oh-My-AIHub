import type { ChannelOfferStatus, ChannelStatus, ValidationStatus } from '../api/contracts'
import { Badge, type BadgeTone } from '../ui'

type State = ChannelStatus | ChannelOfferStatus | ValidationStatus

const labels: Record<State, string> = {
  draft: '草稿',
  published: '已发布',
  paused: '已暂停',
  deleted: '已删除',
  active: '启用',
  disabled: '停用',
  in_progress: '验证中',
  passed: '已通过',
  failed: '失败',
}

const tones: Record<State, BadgeTone> = {
  draft: 'neutral',
  published: 'success',
  paused: 'warning',
  deleted: 'danger',
  active: 'success',
  disabled: 'warning',
  in_progress: 'info',
  passed: 'success',
  failed: 'danger',
}

export function AdminChannelBadge({ status }: { status: State }) {
  return <Badge tone={tones[status]}>{labels[status]}</Badge>
}
