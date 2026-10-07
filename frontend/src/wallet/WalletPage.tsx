import { useQueryClient } from '@tanstack/react-query'
import type { Wallet, WalletRecoveryAction } from '../api/types'
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
        label="冻结中的积分"
        value={`${formatPointAmount(wallet.asset_reserved)} / ${formatPointAmount(wallet.spend_authorized)}`}
      />
    </MetricGrid>
  )
}

const recoveryLabels: Record<WalletRecoveryAction['kind'], string> = {
  market: 'C2C 市场',
  create_buy_order: '发布买单',
  my_orders: '我的挂单',
}

/** 余额不足、超限与信用冻结的提示：原“余额不足”页并入钱包，补足积分入口放在提示条内。 */
export function WalletRiskNotice({
  wallet,
  actions = [],
}: {
  wallet: Wallet
  actions?: WalletRecoveryAction[]
}) {
  if (wallet.risk_status === 'normal') return null
  const recoverable = wallet.risk_status === 'insufficient' || wallet.risk_status === 'over_limit'
  const tone = walletRiskTone(wallet.risk_status)
  return (
    <Notice
      action={
        recoverable && actions.length > 0 ? (
          <span className="wallet-recovery">
            {actions.map((action, index) => (
              <ButtonLink
                key={action.kind}
                size="sm"
                to={action.href}
                variant={index === 0 ? 'primary' : 'secondary'}
              >
                {recoveryLabels[action.kind]}
              </ButtonLink>
            ))}
          </span>
        ) : undefined
      }
      tone={tone === 'success' ? 'success' : tone}
    >
      <strong>{walletRiskLabel(wallet.risk_status)}</strong>
      {' · '}
      {recoverable
        ? `当前可消费 ${formatPointAmount(wallet.spendable_capacity)} 积分，可通过 C2C 补足`
        : '请联系管理员恢复新消费与持有'}
    </Notice>
  )
}

export function WalletPage() {
  const query = useWalletQuery()
  const queryClient = useQueryClient()

  return (
    <>
      <PageHeader title="钱包" />
      <QueryBoundary errorFallback="钱包加载失败" query={query}>
        {({ wallet, recovery_actions }) => (
          <>
            <WalletRiskNotice actions={recovery_actions} wallet={wallet} />
            <WalletSummary wallet={wallet} />
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
        title="账本分录"
      >
        <LedgerEntriesPanel />
      </Card>
    </>
  )
}
