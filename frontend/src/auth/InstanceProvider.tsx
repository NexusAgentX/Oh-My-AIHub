import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { Outlet } from 'react-router-dom'
import { Navigate } from 'react-router-dom'
import { api } from '../api/client'
import { PageLoading } from '../ui'

type InstanceState = {
  ready: boolean
  initialized: boolean | null
  testSite: boolean
  fixedAdminUsername: string | null
  refresh: () => Promise<void>
}

const InstanceContext = createContext<InstanceState>({
  ready: false,
  initialized: null,
  testSite: false,
  fixedAdminUsername: null,
  refresh: async () => undefined,
})

export function InstanceProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<Omit<InstanceState, 'refresh'>>({
    ready: false,
    initialized: null,
    testSite: false,
    fixedAdminUsername: null,
  })

  const refresh = useCallback(async () => {
    try {
      const instance = await api.instanceState()
      setState({ ready: true, initialized: instance.initialized, testSite: instance.test_site, fixedAdminUsername: instance.fixed_admin_username })
    } catch {
      // 探测失败时按已初始化处理，交由会话门卫接管，避免整站卡死。
      setState(previous => ({ ...previous, ready: true, initialized: true }))
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  return (
    <InstanceContext.Provider value={{ ...state, refresh }}>
      {state.testSite && <aside className="test-site-notice" aria-label="测试环境">测试站 · 数据可能随时清空</aside>}
      {children}
    </InstanceContext.Provider>
  )
}

export function useInstance() {
  return useContext(InstanceContext)
}

/** 未初始化实例把一切路由导向初始化步骤；已初始化实例不拦截。 */
export function RequireInitialized() {
  const { ready, initialized } = useInstance()
  if (!ready) {
    return <PageLoading />
  }
  if (initialized === false) return <Navigate replace to="/initialize" />
  return <Outlet />
}
