import { useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import type { Wallet, WalletRecoveryAction } from '../api/contracts'
import {
  Button,
  ButtonLink,
  Card,
  Icon,
  Metric,
  MetricGrid,
  Notice,
  PageHeader,
  QueryBoundary,
} from '../ui'
import { LedgerEntriesPanel } from './LedgerEntriesTable'
import {
  creditUsagePercent,
  formatPointAmount,
  walletRiskLabel,
  walletRiskTone,
} from './presentation'
import { useWalletQuery, walletKeys } from './queries'

export function WalletSummary({ wallet }: { wallet: Wallet }) {
  return (
    <MetricGrid label="钱包摘要">
      <Metric hint="积分" label="已入账余额" value={formatPointAmount(wallet.posted_balance)} />
      <Metric
        hint="积分"
        label="可消费额度"
        tone="accent"
        value={formatPointAmount(wallet.spendable_capacity)}
      />
      <Metric
        hint={`已用 ${formatPointAmount(wallet.credit_used)}`}
        label="信用额度"
        progress={creditUsagePercent(wallet.credit_limit, wallet.credit_used)}
        value={formatPointAmount(wallet.credit_limit)}
      />
      <Metric
        hint="资产冻结 / 消费授权"
        label="持有中的积分"
        value={`${formatPointAmount(wallet.asset_reserved)} / ${formatPointAmount(wallet.spend_authorized)}`}
      />
    </MetricGrid>
  )
}

export function WalletRiskNotice({ wallet }: { wallet: Wallet }) {
  if (wallet.risk_status === 'normal') return null
  const tone = walletRiskTone(wallet.risk_status)
  return (
    <Notice tone={tone === 'success' ? 'success' : tone}>
      <strong>{walletRiskLabel(wallet.risk_status)}</strong>
      {' · '}当前可消费 {formatPointAmount(wallet.spendable_capacity)} 积分
    </Notice>
  )
}

const recoveryCopy: Record<WalletRecoveryAction['kind'], { title: string; meta: string }> = {
  market: { title: '进入 C2C 市场', meta: '查看当前买单和卖单' },
  create_buy_order: { title: '发布买单', meta: '按期望价格等待卖家' },
  my_orders: { title: '查看我的挂单', meta: '管理未成交与进行中订单' },
}

export function RecoveryActions({ actions }: { actions: WalletRecoveryAction[] }) {
  return (
    <div className="recovery-grid">
      {actions.map((action) => (
        <Link className="recovery-card" key={action.kind} to={action.href}>
          <span className="recovery-card-icon"><Icon name="swap" /></span>
          <span>
            <strong>{recoveryCopy[action.kind].title}</strong>
            <small>{recoveryCopy[action.kind].meta}</small>
          </span>
          <Icon name="chevron-right" />
        </Link>
      ))}
    </div>
  )
}

export function WalletPage() {
  const query = useWalletQuery()
  const queryClient = useQueryClient()

  return (
    <>
      <PageHeader
        actions={<ButtonLink to="/c2c" variant="primary">进入 C2C 市场</ButtonLink>}
        description="可用积分、信用额度与不可修改的账本分录"
        title="钱包"
      />
      <QueryBoundary errorFallback="钱包加载失败" query={query}>
        {({ wallet, recovery_actions }) => (
          <>
            <WalletSummary wallet={wallet} />
            <WalletRiskNotice wallet={wallet} />
            {(wallet.risk_status === 'insufficient' || wallet.risk_status === 'over_limit') && (
              <Card title="补足积分">
                <RecoveryActions actions={recovery_actions} />
              </Card>
            )}
            {wallet.risk_status === 'credit_frozen' && (
              <Card title="信用已冻结">
                <p className="muted">请联系管理员恢复新消费与持有。</p>
              </Card>
            )}
          </>
        )}
      </QueryBoundary>
      <Card
        actions={
          <Button
            icon={<Icon name="refresh" />}
            onClick={() => void queryClient.invalidateQueries({ queryKey: walletKeys.all })}
            size="sm"
            variant="quiet"
          >
            刷新
          </Button>
        }
        className="wallet-ledger-card"
        flush
        title="最近分录"
      >
        <LedgerEntriesPanel />
      </Card>
    </>
  )
}
