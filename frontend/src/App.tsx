import { InboxProvider } from './inbox/InboxProvider'
import { MarketHallPage, MarketHallChannelPage } from './market/Hall'
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
import { HomePage } from './home/HomePage'
import { UsagePage } from './usage/UsagePage'
import { BuyRouteDrawer, MyOrdersPage, MyTradesPage, PointsMarketPage, PointsPage, SellRouteDrawer } from './points/PointsPage'
import { TradeDetailPage } from './points/TradeDetailPage'
import { KeysPage } from './keys/KeysPage'
import { KeyCreateDrawer, KeyDetailPage } from './keys/KeyDrawer'
import { KeyRoutingDrawer } from './keys/KeyRouting'
import { clearSecretHandoffs } from './ui/secretHandoff'
import { UnsavedGuardProvider } from './ui/UnsavedGuard'
import { createQueryClient } from './api/query'
import { adminRoutes } from './admin/routes'
import { RouteErrorPage } from './layouts/RouteErrorPage'
import { NotFoundPage } from './layouts/NotFoundPage'
import { ProductLayout } from './layouts/ProductLayout'
import { LegacyRedirect } from './layouts/LegacyRedirect'
import { PageLoading } from './ui'
import { LandingRoute, WelcomePage } from './welcome/WelcomePage'

function RequireSession() {
  const { account, loading } = useAuth()
  if (loading) return <PageLoading />
  if (!account) return <Navigate replace to="/login" />
  return <Outlet />
}

function RequireReadyAccount() {
  const { account } = useAuth()
  if (account?.must_change_password) {
    return <Navigate replace to="/account/password?first=1" />
  }
  return <InboxProvider><Outlet /></InboxProvider>
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
      clearSecretHandoffs()
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
          <UnsavedGuardProvider><Outlet /></UnsavedGuardProvider>
        </AuthProvider>
      </InstanceProvider>
    </QueryClientProvider>
  )
}

export const appRoutes = createRoutesFromElements(
  <Route element={<AppProviders />} errorElement={<RouteErrorPage />} HydrateFallback={PageLoading}>
    <Route element={<InstanceInitializePage />} path="/initialize" />
    <Route element={<RequireInitialized />}>
    <Route element={<WelcomePage />} path="/welcome" />
    <Route element={<LandingRoute />} path="/" />
    <Route element={<NotFoundPage />} path="*" />
    <Route element={<LoginPage />} path="/login" />
    <Route element={<RequireSession />}>
      <Route element={<FirstPasswordChangePage />} path="/account/password" />
      <Route element={<RequireReadyAccount />}>
        <Route element={<ProductLayout />}>
          <Route element={<HomePage />} path="/home" />
          <Route lazy={async()=>({Component:(await import('./inbox/InboxPage')).InboxPage})} path="/inbox" />
          <Route element={<LegacyRedirect from="/channels" to="/supply/channels" />} path="/channels/*" />
          <Route element={<LegacyRedirect from="/contributions" to="/supply/contributions" />} path="/contributions/*" />
          <Route element={<LegacyRedirect from="/detection" to="/supply/detection" />} path="/detection/*" />
          <Route element={<LegacyRedirect from="/forum/tickets" to="/tickets" />} path="/forum/tickets/*" />
          <Route lazy={async()=>({Component:(await import('./market/Pages')).MarketModelsPage})} path="/models" />
          <Route lazy={async()=>({Component:(await import('./market/Pages')).MarketModelDetailPage})} path="/models/:model" />
          <Route element={<KeysPage />} path="/keys"><Route element={<KeyCreateDrawer />} path="new" /></Route>
          <Route element={<KeyDetailPage />} path="/keys/:id"><Route element={<KeyRoutingDrawer />} path="routing/:model" /></Route>
          <Route element={<MarketHallPage />} path="/channel-hall" />
          <Route element={<MarketHallChannelPage />} path="/channel-hall/:id" />
          <Route lazy={async()=>({Component:(await import('./market/Pages')).MarketSuppliersPage})} path="/suppliers" />
          <Route lazy={async()=>({Component:(await import('./market/Pages')).MarketSupplierDetailPage})} path="/suppliers/:id" />
          <Route lazy={async()=>({Component:(await import('./market/Pages')).MarketGroupPage})} path="/groups/:id" />
          <Route lazy={async()=>({Component:(await import('./market/Pages')).MarketReportPage})} path="/reports/:id" />
          <Route lazy={async()=>({Component:(await import('./group-channels/Pages')).GroupChannelsPage})} path="/supply/channels" />
          <Route lazy={async()=>({Component:(await import('./group-channels/RevenuePage')).RevenuePage})} path="/supply/revenue" />
 <Route lazy={async()=>({Component:(await import("./contributions/Pages")).ContributionsPage})} path="/supply/contributions"><Route lazy={async()=>({Component:(await import("./contributions/Pages")).NewContributionRoute})} path="new" /></Route>
 <Route lazy={async()=>({Component:(await import("./contributions/Pages")).ContributionDetailPage})} path="/supply/contributions/:draftID"/>
 <Route lazy={async()=>({Component:(await import('./detection/Pages')).DetectionPage})} path="/supply/detection" />
 <Route lazy={async()=>({Component:(await import('./detection/Pages')).NewDetectionPage})} path="/supply/detection/new" />
 <Route lazy={async()=>({Component:(await import('./detection/Pages')).DetectionDetailPage})} path="/supply/detection/:taskID" />
 <Route lazy={async()=>({Component:(await import('./detection/Pages')).SavedDetectionReportPage})} path="/supply/detection/reports/:reportID" />
          <Route lazy={async()=>({Component:(await import('./group-channels/Pages')).GroupChannelEditorPage})} path="/supply/channels/new" />
          <Route lazy={async()=>({Component:(await import('./group-channels/Pages')).GroupChannelEditorPage})} path="/supply/channels/:id/*" />
          <Route element={<PointsPage />} path="/points" />
          <Route element={<PointsMarketPage />} path="/points/market"><Route element={<BuyRouteDrawer />} path=":orderID" /></Route>
          <Route element={<MyTradesPage />} path="/points/trades" />
          <Route element={<MyOrdersPage />} path="/points/orders"><Route element={<SellRouteDrawer />} path="new" /></Route>
          <Route element={<TradeDetailPage />} path="/points/trades/:id" />
          <Route element={<LegacyRedirect from="/usage" to="/calls" />} path="/usage" />
          <Route element={<UsagePage />} path="/calls" />
          <Route lazy={async () => ({ Component: (await import('./calls/Pages')).CallInsightsPage })} path="/calls/insights" />
          <Route lazy={async () => ({ Component: (await import('./calls/Pages')).CallPage })} path="/calls/:id" />
          <Route element={<LegacyRedirect from="/me" to="/account" />} path="/me" />
          <Route element={<AccountPage />} path="/account" />
          <Route lazy={async () => ({ Component: (await import('./forum/ForumPage')).ForumPage })} path="/forum" />
          <Route lazy={async () => ({ Component: (await import('./forum/ForumPage')).TicketsPage })} path="/tickets" />
          <Route lazy={async () => ({ Component: (await import('./forum/TopicEditorPage')).NewTopicPage })} path="/forum/new" />
          <Route lazy={async () => ({ Component: (await import('./forum/TopicEditorPage')).NewTicketPage })} path="/tickets/new" />
          <Route lazy={async () => ({ Component: (await import('./forum/TopicPage')).TopicPage })} path="/tickets/:id" />
          <Route element={<LegacyRedirect from="/forum/boards" to="/admin/forum/boards" />} path="/forum/boards" />
          <Route lazy={async () => ({ Component: (await import('./forum/TopicPage')).TopicPage })} path="/forum/topics/:id" />
          <Route lazy={async () => ({ Component: (await import('./forum/TopicEditorPage')).EditTopicPage })} path="/forum/topics/:id/edit" />
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
