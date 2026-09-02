import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { api, ApiError, setAuthFailureHandler } from '../api/client'
import type { Account } from '../api/contracts'

type AuthContextValue = {
  account: Account | null
  loading: boolean
  sessionError: string
  login: (username: string, password: string) => Promise<Account>
  logout: () => Promise<void>
  changePassword: (
    currentPassword: string,
    newPassword: string,
  ) => Promise<Account>
  refresh: () => Promise<Account | null>
  synchronizeAccount: (account: Account) => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [account, setAccount] = useState<Account | null>(null)
  const [loading, setLoading] = useState(true)
  const [sessionError, setSessionError] = useState('')

  const refresh = useCallback(async () => {
    try {
      const current = await api.session()
      setAccount(current)
      setSessionError('')
      return current
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        setAccount(null)
        return null
      }
      throw error
    }
  }, [])

  useEffect(() => {
    setAuthFailureHandler((error) => {
      if (error.code === 'authentication_required') {
        setAccount(null)
        return
      }
      void refresh().catch(() => {
        setSessionError('无法刷新账户状态，请稍后重试')
      })
    })
    void refresh()
      .catch(() => setSessionError('服务暂时不可用，请稍后重试'))
      .finally(() => setLoading(false))
    return () => setAuthFailureHandler(null)
  }, [refresh])

  const value = useMemo<AuthContextValue>(
    () => ({
      account,
      loading,
      sessionError,
      async login(username, password) {
        const current = await api.login(username, password)
        setAccount(current)
        setSessionError('')
        return current
      },
      async logout() {
        await api.logout()
        setAccount(null)
      },
      async changePassword(currentPassword, newPassword) {
        const current = await api.changePassword(currentPassword, newPassword)
        setAccount(current)
        return current
      },
      refresh,
      synchronizeAccount(updated) {
        setAccount((current) => current?.id === updated.id ? updated : current)
      },
    }),
    [account, loading, refresh, sessionError],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const value = useContext(AuthContext)
  if (!value) throw new Error('useAuth must be used inside AuthProvider')
  return value
}
