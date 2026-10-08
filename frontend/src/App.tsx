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
import { AccountPage } from './account/AccountPage'
import { MePage } from './account/MePage'
import { ChannelEditorPage } from './channels/ChannelEditorPage'
import { ChannelsPage } from './channels/ChannelsPage'
import { HomePage } from './home/HomePage'
import { UsagePage } from './usage/UsagePage'
import { PointsPage } from './points/PointsPage'
import { TradeDetailPage } from './points/TradeDetailPage'
import { KeysPage } from './keys/KeysPage'
import { ModelDetailPage } from './models/ModelDetailPage'
import { ModelsPage } from './models/ModelsPage'
import { createQueryClient } from './api/query'
import { ProductLayout } from './layouts/ProductLayout'
import { LoadingState } from './ui'
import { WelcomePage } from './welcome/WelcomePage'

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
    <Route element={<WelcomePage />} path="/" />
    <Route element={<WelcomePage />} path="*" />
    <Route element={<LoginPage />} path="/login" />
    <Route element={<RequireSession />}>
      <Route element={<FirstPasswordChangePage />} path="/account/password" />
      <Route element={<RequireReadyAccount />}>
        <Route element={<ProductLayout />}>
          <Route element={<HomePage />} path="/home" />
          <Route element={<ModelsPage />} path="/models" />
          <Route element={<ModelDetailPage />} path="/models/:model" />
          <Route element={<KeysPage />} path="/keys" />
          <Route element={<ChannelsPage />} path="/channels" />
          <Route element={<ChannelEditorPage />} path="/channels/new" />
          <Route element={<ChannelEditorPage />} path="/channels/:id" />
          <Route element={<PointsPage />} path="/points" />
          <Route element={<TradeDetailPage />} path="/points/trades/:id" />
          <Route element={<UsagePage />} path="/usage" />
          <Route element={<MePage />} path="/me" />
          <Route element={<AccountPage />} path="/account" />
        </Route>
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
