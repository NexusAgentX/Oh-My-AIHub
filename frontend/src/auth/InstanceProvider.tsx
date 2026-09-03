import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { Outlet } from 'react-router-dom'
import { Navigate } from 'react-router-dom'
import { api } from '../api/client'
import { LoadingState } from '../ui/FormControls'

type InstanceState = {
  ready: boolean
  initialized: boolean | null
  refresh: () => Promise<void>
}

const InstanceContext = createContext<InstanceState>({
  ready: false,
  initialized: null,
  refresh: async () => undefined,
})

export function InstanceProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<{ ready: boolean; initialized: boolean | null }>({
    ready: false,
    initialized: null,
  })

  const refresh = useCallback(async () => {
    try {
      const instance = await api.instanceState()
      setState({ ready: true, initialized: instance.initialized })
    } catch {
      // 探测失败时按已初始化处理，交由会话门卫接管，避免整站卡死。
      setState({ ready: true, initialized: true })
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  return (
    <InstanceContext.Provider value={{ ready: state.ready, initialized: state.initialized, refresh }}>
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
    return (
      <main className="route-loading">
        <LoadingState />
      </main>
    )
  }
  if (initialized === false) return <Navigate replace to="/initialize" />
  return <Outlet />
}
