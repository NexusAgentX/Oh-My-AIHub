import type {
  LedgerCounterparty,
  LedgerEntry,
  WalletRiskStatus,
} from '../api/contracts'
import { formatNanoPoints, parseNanoPoints } from '../money/amount'

const transactionLabels: Record<string, string> = {
  transfer: '积分结算',
  hold_capture: '预授权结算',
  self_channel_usage: '自有渠道调用',
  admin_adjustment: '管理员调整',
  bad_debt_transfer: '坏账转移',
  reversal: '交易冲正',
}

const systemAccountLabels: Record<string, string> = {
  platform_incentive: '平台激励账户',
  platform_loss: '平台损失账户',
}

export function formatPointAmount(value: string, signed = false) {
  const amount = parseNanoPoints(value)
  const formatted = formatNanoPoints(amount)
  return signed && amount > 0n ? `+${formatted}` : formatted
}

export function ledgerEntryLabel(entry: LedgerEntry) {
  return transactionLabels[entry.transaction_kind] ?? entry.transaction_kind
}

export function counterpartyLabel(counterparty: LedgerCounterparty) {
  if (counterparty.account_kind !== 'user') {
    return systemAccountLabels[counterparty.account_kind] ?? counterparty.account_kind
  }
  return counterparty.account_id ? `账户 ${counterparty.account_id.slice(0, 8)}` : '账户'
}

export function ledgerCounterparties(entry: LedgerEntry) {
  if (entry.counterparties.length === 0) return '—'
  return entry.counterparties.map(counterpartyLabel).join('、')
}

export function walletRiskLabel(status: WalletRiskStatus) {
  const labels: Record<WalletRiskStatus, string> = {
    normal: '正常',
    insufficient: '可消费额度不足',
    over_limit: '信用超限',
    credit_frozen: '信用冻结',
  }
  return labels[status]
}

export function walletRiskTone(status: WalletRiskStatus) {
  return status === 'normal' ? 'success' : status === 'insufficient' ? 'warning' : 'danger'
}
