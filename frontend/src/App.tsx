import { QueryClientProvider, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import {
  createBrowserRouter,
  createRoutesFromElements,
  Navigate,
  Outlet,
  Route,
  RouterProvider,
} from 'react-router-dom'
import { AuthProvider, useAuth } from './auth/AuthProvider'
import { InstanceInitializePage } from './auth/InstanceInitializePage'
import { InstanceProvider, RequireInitialized } from './auth/InstanceProvider'
import { FirstPasswordChangePage } from './auth/FirstPasswordChangePage'
import { LoginPage } from './auth/LoginPage'
import { HomePage } from './home/HomePage'
import { createQueryClient } from './api/query'
import { adminRoutes } from './admin/routes'
import { ProductLayout } from './layouts/ProductLayout'
import { LoadingState } from './ui'
import { LandingRoute, WelcomePage } from './welcome/WelcomePage'

function RequireSession() {
  const { account, loading } = useAuth()
  if (loading) return <FullPageLoading />
  if (!account) return <Navigate replace to="/login" />
  return <Outlet />
}

function RequireReadyAccount() {
  const { account } = useAuth()
  if (account?.must_change_password) {
    return <Navigate replace to="/account/password?first=1" />
  }
  return <Outlet />
}

function FullPageLoading() {
  return (
    <main className="route-loading">
      <LoadingState />
    </main>
  )
}

const queryClient = createQueryClient()

/** 登录、退出或切换账号时清空查询缓存，避免上一个账号的数据残留。 */
function SessionQueryReset() {
  const { account } = useAuth()
  const client = useQueryClient()
  const accountID = account?.id ?? ''
  const previous = useRef(accountID)
  useEffect(() => {
    if (previous.current !== accountID) {
      previous.current = accountID
      client.clear()
    }
  }, [accountID, client])
  return null
}

function AppProviders() {
  return (
    <QueryClientProvider client={queryClient}>
      <InstanceProvider>
        <AuthProvider>
          <SessionQueryReset />
          <Outlet />
        </AuthProvider>
      </InstanceProvider>
    </QueryClientProvider>
  )
}

export const appRoutes = createRoutesFromElements(
  <Route element={<AppProviders />}>
    <Route element={<InstanceInitializePage />} path="/initialize" />
    <Route element={<RequireInitialized />}>
    <Route element={<WelcomePage />} path="/welcome" />
    <Route element={<LandingRoute />} path="/" />
    <Route element={<LandingRoute />} path="*" />
    <Route element={<LoginPage />} path="/login" />
    <Route element={<RequireSession />}>
      <Route element={<FirstPasswordChangePage />} path="/account/password" />
      <Route element={<RequireReadyAccount />}>
        <Route element={<ProductLayout />}>
          <Route element={<HomePage />} path="/home" />
        </Route>
        {adminRoutes}
      </Route>
    </Route>
    </Route>
  </Route>,
)

let router: ReturnType<typeof createBrowserRouter> | undefined

export function App() {
  router ??= createBrowserRouter(appRoutes)
  return <RouterProvider router={router} />
}
