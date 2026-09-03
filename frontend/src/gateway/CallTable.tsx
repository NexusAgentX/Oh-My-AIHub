import { Link } from 'react-router-dom'
import type { GatewayCall } from '../api/contracts'
import { formatDate, protocolLabels } from '../channels/presentation'
import { GatewayStatusBadge, shortID, totalTokens } from './presentation'

export function CallTable({ calls }: { calls: GatewayCall[] }) {
  if (calls.length === 0) return <div className="empty-state">暂无调用记录</div>
  return (
    <>
      <div className="desktop-table-wrap">
        <table className="data-table gateway-call-table">
          <thead>
            <tr>
              <th scope="col">调用 / 模型</th>
              <th scope="col">状态</th>
              <th scope="col">API 格式</th>
              <th scope="col">Key</th>
              <th scope="col">渠道</th>
              <th scope="col">尝试</th>
              <th scope="col">Tokens</th>
              <th scope="col">时间</th>
            </tr>
          </thead>
          <tbody>
            {calls.map((call) => (
              <tr key={call.id}>
                <td>
                  <Link className="table-row-link" to={`/calls/${call.id}`}>
                    <strong>{call.model_id || '受限调用视图'}</strong>
                    <small className="mono-value">{shortID(call.id)}</small>
                    <span className="visually-hidden">调用详情</span>
                  </Link>
                </td>
                <td><GatewayStatusBadge status={call.status} /></td>
                <td>{call.protocol ? protocolLabels[call.protocol] : '—'}</td>
                <td className="mono-value">{call.key_prefix ? `${call.key_prefix}…` : '—'}</td>
                <td>{call.final_channel_name || '—'}</td>
                <td>{call.attempt_count}</td>
                <td>{totalTokens(call)}</td>
                <td>{formatDate(call.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="mobile-card-list">
        {calls.map((call) => (
          <Link className="mobile-data-card mobile-data-card-link" key={call.id} to={`/calls/${call.id}`}>
            <header>
              <div><strong>{call.model_id || '受限调用视图'}</strong><span className="mono-value">{shortID(call.id)}</span></div>
              <GatewayStatusBadge status={call.status} />
            </header>
            <dl>
              <div><dt>API 格式</dt><dd>{call.protocol ? protocolLabels[call.protocol] : '—'}</dd></div>
              <div><dt>渠道</dt><dd>{call.final_channel_name || '—'}</dd></div>
              <div><dt>尝试 / Tokens</dt><dd>{call.attempt_count} / {totalTokens(call)}</dd></div>
              <div><dt>时间</dt><dd>{formatDate(call.created_at)}</dd></div>
            </dl>
            <span className="visually-hidden">调用详情</span>
          </Link>
        ))}
      </div>
    </>
  )
}
