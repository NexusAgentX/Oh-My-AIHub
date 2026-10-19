import type { Inbox, InboxItem, InboxSource } from './model'
export const demoTradeID = '80000000-0000-4000-8000-000000001560'
const stamp = '2026-10-19T04:00:00Z'
const item = (id: string, category: InboxItem['category'], title: string, message: string, href: string, blocker = false): InboxItem => ({ id, category, title, message, href, blocker, updated_at: stamp, deadline: null })
function source(id: InboxSource['id'], label: string, href: string, items: InboxItem[], workspace: InboxSource['workspace'] = 'member'): InboxSource {
  return { id, label, href, items, workspace, state: 'complete', counts: { action: items.filter(x => x.category === 'action').length, anomaly: items.filter(x => x.category === 'anomaly').length, progress: items.filter(x => x.category === 'progress').length } }
}
export function inboxFixture(now = Date.now()): Inbox {
  const trade = item(demoTradeID, 'action', '积分交易 · 示例待付款', '待付款，请查看收款信息并完成付款。', `/points/trades/${demoTradeID}`)
  trade.deadline = new Date(now + 12 * 60_000).toISOString()
  return { generated_at: new Date(now).toISOString(), sources: [
    source('trades', '积分交易', '/points/trades', [trade]),
    source('channels', '我的渠道', '/supply/channels', [item('demo-channel', 'anomaly', '示例共享渠道', '没有可用地址，请重新选择分组地址。', '/supply/channels/demo-channel/config', true)]),
    source('keys', 'API Key', '/keys', [item('demo-key', 'anomaly', '示例应用 Key', '本月预算已使用至少 80%', '/keys/demo-key')]),
    source('points', '钱包', '/points', []),
    source('contributions', '我的贡献', '/supply/contributions', [item('demo-draft', 'action', '示例供应商修订', '内容与检测已就绪，可以提交。', '/supply/contributions/demo-draft')]),
    source('detection', '检测任务', '/supply/detection', [item('demo-detection', 'progress', 'demo-chat 能力检测', '检测进行中 7/12', '/supply/detection/demo-detection')]),
    source('reviews', '贡献审核', '/admin/contributions', [item('demo-review', 'action', '示例分组报价修订', '待审核，请查看内容与检测证据。', '/admin/contributions/demo-review')], 'admin'),
    source('governance', '治理提醒', '/admin', [item('demo-risk', 'anomaly', '1 个账户超出信用额度', '查看治理详情', '/admin/users', true)], 'admin'),
  ] }
}
