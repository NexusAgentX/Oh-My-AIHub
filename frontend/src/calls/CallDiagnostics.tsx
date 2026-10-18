import type { components } from '../api/schema.gen'
import { formatMs } from '../money/format'
const labels: Record<string, string> = {
  authentication: '鉴权',
  request: '读取与解析请求',
  open_call: '创建调用',
  admission: '模型与额度检查',
  routing: '路由',
  pricing: '计价',
  settlement: '记账',
  client_write: '写入客户端',
  stop: '正常结束',
  length: '达到长度上限',
  tool_call: '工具调用',
  content_filter: '内容拦截',
  incomplete: '未完成',
  other: '其他',
  reported: '已读到',
  missing: '未读到',
  parse_limit: '解析超限',
  not_observed: '未观察',
  complete: '完整交付',
  client_disconnected: '客户端断开',
  interrupted: '中断',
  not_started: '未开始',
  excluded: '用户排除',
  not_selected: '未勾选',
  cooldown: '冷却中',
  concurrency: '并发上限',
  rpm: '请求速率上限',
  revenue: '收入上限',
  daily_revenue: '收入上限',
  demoted: '异常降级',
  sticky: '沿用上次渠道',
  cheapest: '价格优先',
  reliable: '稳定优先',
  fastest: '延迟优先',
  speed: '速率优先',
  manual: '手动顺序',
  eligible: '可用',
  required_capability_evidence_missing: '所需能力或版本证据不足',
}
export function diagnosticLabel(value: string) {
  return labels[value] ?? value
}
export function CallDiagnostics({ data }: { data: components['schemas']['CallDiagnostics'] }) {
  return (
    <>
      <section className="call-detail-section">
        <h3>交付与用量</h3>
        <dl className="call-detail-grid">
          <div>
            <dt>交付</dt>
            <dd>{diagnosticLabel(data.delivery)}</dd>
          </div>
          <div>
            <dt>用量</dt>
            <dd>{diagnosticLabel(data.usage_status)}</dd>
          </div>
          {data.stop_reason && (
            <div>
              <dt>模型结束原因</dt>
              <dd>{diagnosticLabel(data.stop_reason)}</dd>
            </div>
          )}
          <div>
            <dt>上游已读</dt>
            <dd>{data.upstream_bytes.toLocaleString()} bytes</dd>
          </div>
          {data.frames > 0 && (
            <div>
              <dt>数据帧</dt>
              <dd>{data.frames.toLocaleString()}</dd>
            </div>
          )}
          {data.max_frame_gap_ms !== null && (
            <div>
              <dt>最大帧间隔</dt>
              <dd>{formatMs(data.max_frame_gap_ms)}</dd>
            </div>
          )}
        </dl>
      </section>
      {data.stages.length > 0 && (
        <section className="call-detail-section">
          <h3>网关阶段耗时</h3>
          <dl className="call-detail-grid">
            {data.stages.map((s, i) => (
              <div key={`${s.stage}-${i}`}>
                <dt>{diagnosticLabel(s.stage)}</dt>
                <dd>{s.duration_ms.toFixed(2)} ms</dd>
              </div>
            ))}
          </dl>
          <small className="muted">客户端写入与上游响应期间重叠；阶段耗时不直接相加。</small>
        </section>
      )}
      <section className="call-detail-section">
        <h3>路由决策</h3>
        <dl className="call-detail-grid">
          <div>
            <dt>最多尝试</dt>
            <dd>{data.max_attempts || '尚未选路'}</dd>
          </div>
          <div>
            <dt>调用时间上限</dt>
            <dd>{formatMs(data.call_timeout_ms)}</dd>
          </div>
          <div>
            <dt>断连续读上限</dt>
            <dd>{formatMs(data.drain_timeout_ms)}</dd>
          </div>
        </dl>
        <ul className="call-route-decisions">
          {data.routing.map((d) => (
            <li key={d.channel.id}>
              <strong>{d.channel.name}</strong>
              <span>
                {d.rank > 0 ? `候选 #${d.rank} · ` : ''}
                {diagnosticLabel(d.reason)}
                {d.missing_capability && <small> · {d.missing_capability}</small>}
              </span>
            </li>
          ))}
        </ul>
      </section>
    </>
  )
}
