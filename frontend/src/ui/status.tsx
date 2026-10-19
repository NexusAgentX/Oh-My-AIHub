import type { TradeStatus } from '../api/types'
import { Badge, type BadgeTone } from './Badge'
export type StateMeta = { label: string; tone: BadgeTone; terminal: boolean }
const state = (label: string, tone: BadgeTone = 'neutral', terminal = false): StateMeta => ({ label, tone, terminal })
export const statusRegistry = {
  trade: {
    awaiting_payment: state('待付款', 'warning'), paid: state('已付款', 'info'), disputed: state('申诉中', 'danger'),
    released: state('已完成', 'success', true), cancelled: state('已取消', 'neutral', true), resolved_to_buyer: state('判给买家', 'success', true), resolved_to_seller: state('退回卖家', 'neutral', true),
  } satisfies Record<TradeStatus, StateMeta>,
  contribution: {
    draft: state('草稿'), detecting: state('检测中', 'info'), invalid: state('请补充内容', 'warning'), ready: state('可提交', 'warning'), submitted: state('待审核', 'info'), rejected: state('已驳回', 'warning'), withdrawn: state('已撤回'), publication_pending: state('发布待完成', 'info'), approved: state('已批准', 'success', true), conflict: state('生效版本已变化', 'warning'), supplier_has_groups: state('供应商仍有分组', 'warning'), suite_pending: state('检测套件待实现', 'info'), report_required: state('需要完整报告', 'warning'),
  },
  detection: {
    queued: state('排队中', 'info'), running: state('检测中', 'info'), finished: state('已结束', 'neutral', true), cancelled: state('已取消', 'neutral', true), interrupted: state('已中断', 'danger', true), suite_pending: state('检测套件待实现', 'info', true), failed: state('检测器异常', 'danger', true), outdated: state('配置已变化', 'warning', true), pass: state('通过', 'success', true), partial: state('部分支持', 'warning', true), fail: state('未通过', 'danger', true), unsupported: state('不支持', 'neutral', true), inconclusive: state('无法确认', 'neutral', true), unobserved: state('未观测', 'neutral', true), untested: state('未执行', 'neutral', true),
  },
} satisfies Record<string, Record<string, StateMeta>>
export function statusMeta(domain: keyof typeof statusRegistry, value: string): StateMeta {
  return (statusRegistry[domain] as Record<string, StateMeta>)[value] ?? state(value)
}
export function DomainStatusBadge({ domain, value }: { domain: keyof typeof statusRegistry; value: string }) {
  const meta = statusMeta(domain, value)
  return <Badge tone={meta.tone}>{meta.label}</Badge>
}
