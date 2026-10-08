import type { ModelChannel, RoutingMode, RoutingPreference, RoutingPreferenceInput } from '../api/types'

/** 路由编辑器的本地草稿。order 只在手动模式下提交。 */
export type RoutingDraft = {
  mode: RoutingMode
  order: string[]
  excluded: string[]
  max_attempts: number | null
  ttft_timeout_ms: number | null
}

/**
 * 从已保存的偏好生成草稿：order 去掉已不存在的渠道，并把新渠道追加到末尾；
 * 手动模式下新上架的渠道默认不勾选。
 */
export function draftFromPreference(preference: RoutingPreference, channels: ModelChannel[]): RoutingDraft {
  const ids = channels.map((channel) => channel.id)
  const known = preference.order.filter((id) => ids.includes(id))
  const added = ids.filter((id) => !known.includes(id))
  const manualWithOrder = preference.mode === 'manual' && preference.order.length > 0
  const excluded = preference.excluded.filter((id) => ids.includes(id))
  return {
    mode: preference.mode,
    order: [...known, ...added],
    excluded: manualWithOrder ? [...new Set([...excluded, ...added])] : excluded,
    max_attempts: preference.max_attempts,
    ttft_timeout_ms: preference.ttft_timeout_ms,
  }
}

/** 编辑器中的渠道顺序：手动模式按 order，其余模式保持服务端顺序。 */
export function orderedChannels(channels: ModelChannel[], draft: RoutingDraft) {
  if (draft.mode !== 'manual') return channels
  const position = new Map(draft.order.map((id, index) => [id, index]))
  return channels
    .slice()
    .sort((a, b) => (position.get(a.id) ?? Number.MAX_SAFE_INTEGER) - (position.get(b.id) ?? Number.MAX_SAFE_INTEGER))
}

/** 把 id 移动 delta 位（键盘上下）；越界时不变。 */
export function moveChannel(order: string[], id: string, delta: number) {
  const from = order.indexOf(id)
  const to = from + delta
  if (from < 0 || to < 0 || to >= order.length) return order
  return moveTo(order, id, to)
}

/** 把 id 移到 target 位置（拖拽）。 */
export function moveTo(order: string[], id: string, target: number) {
  const from = order.indexOf(id)
  if (from < 0) return order
  const next = order.slice()
  next.splice(from, 1)
  next.splice(Math.max(0, Math.min(target, next.length)), 0, id)
  return next
}

/** 勾选（使用）或取消勾选某个渠道。 */
export function setUsed(draft: RoutingDraft, id: string, used: boolean): RoutingDraft {
  const excluded = used ? draft.excluded.filter((item) => item !== id) : [...new Set([...draft.excluded, id])]
  return { ...draft, excluded }
}

export function changeMode(draft: RoutingDraft, mode: RoutingMode, channels: ModelChannel[]): RoutingDraft {
  if (mode !== 'manual' || draft.order.length > 0) return { ...draft, mode }
  return { ...draft, mode, order: channels.map((channel) => channel.id) }
}

export function toRoutingInput(draft: RoutingDraft): RoutingPreferenceInput {
  return {
    mode: draft.mode,
    order: draft.mode === 'manual' ? draft.order : [],
    excluded: draft.excluded,
    max_attempts: draft.max_attempts,
    ttft_timeout_ms: draft.ttft_timeout_ms,
  }
}

export function sameDraft(a: RoutingDraft, b: RoutingDraft) {
  return JSON.stringify(toRoutingInput(a)) === JSON.stringify(toRoutingInput(b))
}

/** 高级项中偏离平台默认（null）的数量。 */
export function advancedChanged(draft: RoutingDraft) {
  return (draft.max_attempts === null ? 0 : 1) + (draft.ttft_timeout_ms === null ? 0 : 1)
}

export function channelStateLabel(channel: ModelChannel) {
  if (channel.state === 'cooldown') {
    const minutes = Math.max(1, Math.ceil((channel.cooldown_remaining_seconds ?? 60) / 60))
    return { label: `冷却中 ${minutes} 分钟`, tone: 'warning' as const }
  }
  if (channel.state === 'limited') {
    const remaining = channel.daily_cap_remaining
    if (remaining === null || remaining === undefined) return { label: '已达上限', tone: 'warning' as const }
    return { label: `今日额度剩 ${Math.floor(Number(remaining) * 100)}%`, tone: 'warning' as const }
  }
  return { label: '在线', tone: 'success' as const }
}
