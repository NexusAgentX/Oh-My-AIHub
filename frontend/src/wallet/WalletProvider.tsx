import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { api, ApiError } from '../api/client'
import type { Wallet, WalletRecoveryAction } from '../api/contracts'
import { useAuth } from '../auth/AuthProvider'

type WalletContextValue = {
  wallet: Wallet | null
  recoveryActions: WalletRecoveryAction[]
  loading: boolean
  error: string
  refresh: () => Promise<void>
}

const WalletContext = createContext<WalletContextValue | null>(null)

export function WalletProvider({ children }: { children: ReactNode }) {
  const { account } = useAuth()
  const [wallet, setWallet] = useState<Wallet | null>(null)
  const [recoveryActions, setRecoveryActions] = useState<
    WalletRecoveryAction[]
  >([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const requestGeneration = useRef(0)

  const accountID = account?.id ?? ''
  const accountVersion = account?.version ?? 0
  const passwordChangeRequired = account?.must_change_password ?? false

  const refresh = useCallback(async () => {
    if (!accountID || passwordChangeRequired) return
    const generation = ++requestGeneration.current
    setLoading(true)
    setError('')
    try {
      const response = await api.wallet()
      if (generation !== requestGeneration.current) return
      setWallet(response.wallet)
      setRecoveryActions(response.recovery_actions)
    } catch (caught) {
      if (generation !== requestGeneration.current) return
      setError(caught instanceof ApiError ? caught.message : '钱包加载失败')
    } finally {
      if (generation === requestGeneration.current) setLoading(false)
    }
  }, [accountID, accountVersion, passwordChangeRequired])

  useEffect(() => {
    requestGeneration.current += 1
    setWallet(null)
    setRecoveryActions([])
    setError('')
    setLoading(false)
    if (accountID && !passwordChangeRequired) void refresh()
    return () => {
      requestGeneration.current += 1
    }
  }, [accountID, passwordChangeRequired, refresh])

  const value = useMemo<WalletContextValue>(
    () => ({ wallet, recoveryActions, loading, error, refresh }),
    [error, loading, recoveryActions, refresh, wallet],
  )

  return (
    <WalletContext.Provider value={value}>{children}</WalletContext.Provider>
  )
}

export function useWallet() {
  const value = useContext(WalletContext)
  if (!value) throw new Error('useWallet must be used inside WalletProvider')
  return value
}
