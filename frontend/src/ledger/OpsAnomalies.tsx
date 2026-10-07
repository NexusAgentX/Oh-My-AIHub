import { Link } from 'react-router-dom'
import { Badge, Card, QueryBoundary } from '../ui'
import { drilldownPath } from './opsFormat'
import { useOpsAnomaliesQuery } from './queries'

/** 硬异常与关注项：无内容时整块不渲染，下钻链接固定为后端给出的路径。 */
export function OpsAnomalies() {
  const query = useOpsAnomaliesQuery()
  return (
    <QueryBoundary
      errorFallback="异常信息加载失败"
      isEmpty={(data) =>
        data.hard_anomalies.length === 0 && !data.attention_items.some((item) => item.count > 0)
      }
      query={query}
    >
      {(data) => (
        <Card
          actions={<Badge tone={data.hard_count > 0 ? 'danger' : 'neutral'}>硬异常 {data.hard_count}</Badge>}
          className="ops-anomalies"
          title="异常与关注"
        >
          <ul className="ops-anomaly-list">
            {data.hard_anomalies.map((item) => (
              <li className="ops-anomaly ops-anomaly-hard" key={item.kind}>
                <Badge tone="danger">硬异常</Badge>
                <span>{item.detail}</span>
                <strong className="num">{item.count}</strong>
                <Link to={drilldownPath(item.drilldown)}>下钻</Link>
              </li>
            ))}
            {data.attention_items
              .filter((item) => item.count > 0)
              .map((item) => (
              <li className="ops-anomaly" key={item.kind}>
                <Badge tone="warning">关注</Badge>
                <span>{item.detail}</span>
                <strong className="num">{item.count}</strong>
                {item.drilldown.startsWith('/admin') ? (
                  <Link to={drilldownPath(item.drilldown)}>查看</Link>
                ) : (
                  <span />
                )}
              </li>
            ))}
          </ul>
        </Card>
      )}
    </QueryBoundary>
  )
}
