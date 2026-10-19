import { CallSectionNav } from '../calls/CallSectionNav'
import { Link, Outlet, useLocation } from 'react-router-dom'
import { formatTime } from '../money/time'
import type { ApiKey } from '../api/types'
import { formatPoints } from '../money/format'
import {
  Badge,
  ButtonLink,
  EmptyState,
  Icon,
  Notice,
  PageHeader,
  ProgressBar,
  QueryBoundary,
} from '../ui'
import { budgetProgress, keyBadges } from './keyForm'
import { maxKeys, useKeys } from './queries'

function KeyRow({ apiKey }: { apiKey: ApiKey }) {
  const progress = budgetProgress(apiKey)
  const expired = apiKey.expires_at !== null && new Date(apiKey.expires_at).getTime() < Date.now()
  return (
    <li className="key-row">
      <Link className="key-row-button" to={`/keys/${apiKey.id}`}>
        <span className="key-row-name">
          <strong>{apiKey.name}</strong>
          <code className="mono">{apiKey.prefix}…</code><small className="muted">{apiKey.last_used_at ? `最近使用 ${formatTime(apiKey.last_used_at)}` : '从未使用'}</small>
          <span className="key-row-badges">
            {keyBadges(apiKey).map((badge) => (
              <Badge key={badge} tone="info">
                {badge}
              </Badge>
            ))}
          </span>
        </span>
        <dl className="key-row-spend">
          <div>
            <dt>今日</dt>
            <dd className="num">{formatPoints(apiKey.spend.today)}</dd>
          </div>
          <div>
            <dt>本月</dt>
            <dd className="num">{formatPoints(apiKey.spend.month)}</dd>
          </div>
        </dl>
        <span className="key-row-budget">
          <span className="key-row-budget-label">{progress ? `${progress.label}预算` : '预算'}</span>
          <strong className="num">{progress ? progress.percent >= 100 ? '超预算，调用将被拒' : `已用 ${Math.round(progress.percent)}%` : '不限'}</strong>
          {progress && (
            <ProgressBar label={`${progress.label}预算已用 ${Math.round(progress.percent)}%`} value={progress.percent} warn={progress.warn} />
          )}
        </span>
        <span className="key-row-status">
          {expired ? (
            <Badge tone="danger">已过期</Badge>
          ) : (
            <Badge tone={apiKey.status === 'enabled' ? 'success' : 'neutral'}>
              {apiKey.status === 'enabled' ? '启用' : '停用'}
            </Badge>
          )}
          <Icon name="chevron-right" />
        </span>
      </Link>
    </li>
  )
}

export function KeysPage() {
  const keys = useKeys()
  const location = useLocation()
  const count = keys.data?.items.length ?? 0
  const full = count >= maxKeys
  return (
    <>
      <PageHeader
        actions={
          <ButtonLink aria-disabled={!keys.data || full} onClick={event => { if (!keys.data || full) event.preventDefault() }} icon={<Icon name="plus" />} to="/keys/new" state={{ overlayFrom: location.pathname + location.search }} variant="primary">
            新建 Key
          </ButtonLink>
        }
        title="API Key"
      />
      <CallSectionNav />
      {full && <Notice tone="info">已达到 {maxKeys} 把上限，删除不用的 Key 后再新建</Notice>}
      <QueryBoundary
        empty={<EmptyState title="还没有 Key" />}
        errorFallback="Key 列表加载失败"
        isEmpty={(data) => data.items.length === 0}
        query={keys}
      >
        {(data) => (
          <ul className="key-list" aria-label="API Key">
            {data.items.map((item) => (
              <KeyRow apiKey={item} key={item.id} />
            ))}
          </ul>
        )}
      </QueryBoundary>
      <Outlet />
    </>
  )
}
