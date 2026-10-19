import { LegacyRedirect } from '../layouts/LegacyRedirect'
import { Navigate, Outlet, Route } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'

/** 非管理员访问 /admin/** 时回到产品首页；治理入口由工作区切换器提供。 */
function RequireAdmin() {
  const { account } = useAuth()
  if (!account?.is_admin) return <Navigate replace to="/home" />
  return <Outlet />
}

/** 管理后台路由，挂在 App.tsx 已登录且完成首次改密的分支下；页面按需加载，权限由接口与工作区守卫校验。 */
export const adminRoutes = (
  <Route element={<RequireAdmin />}>
    <Route lazy={async () => ({ Component: (await import('./AdminFrame')).AdminFrame })} path="/admin">
      <Route lazy={async()=>({Component:(await import("../contributions/Pages")).ContributionQueuePage})} path="contributions"><Route lazy={async()=>({Component:(await import("../contributions/Pages")).NewContributionRoute})} path="new" /></Route>
 <Route lazy={async()=>({Component:(await import("../contributions/Pages")).ContributionDetailPage})} path="contributions/:draftID"/>
 <Route lazy={async () => ({ Component: (await import('../forum/BoardsPage')).BoardsPage })} path="forum/boards" />
      <Route index lazy={async () => ({ Component: (await import('./OverviewPage')).OverviewPage })} />
      <Route lazy={async () => ({ Component: (await import('./CallsPage')).CallsPage })} path="calls" />
      <Route lazy={async () => ({ Component: (await import('../calls/Pages')).AdminCallInsightsPage })} path="calls/insights" />
      <Route lazy={async () => ({ Component: (await import('../calls/Pages')).CallPage })} path="calls/:id" />
      <Route lazy={async () => ({ Component: (await import('./PointsPage')).PointsPage })} path="points" />
      <Route path="points/transactions" lazy={async () => ({ Component: (await import('./PointsPage')).AdminTransactionsPage })} />
      <Route path="audit" lazy={async () => ({ Component: (await import('./PointsPage')).AuditPage })} />
      <Route path="disputes/*" element={<LegacyRedirect from="/admin/disputes" to="/admin/trades" />} />
      <Route path="typical-costs" element={<LegacyRedirect from="/admin/typical-costs" to="/admin/settings/typical-costs" />} />
      <Route path="detection-search" element={<LegacyRedirect from="/admin/detection-search" to="/admin/settings/detection" />} />
      <Route lazy={async () => ({ Component: (await import('./UsersPage')).UsersPage })} path="users"><Route path="new" lazy={async () => ({ Component: (await import('./UsersPage')).NewUserRoute })} /><Route path=":id" lazy={async () => ({ Component: (await import('./UsersPage')).UserRoute })} /></Route>
      <Route path="catalog" lazy={async () => ({ Component: (await import('./CatalogPage')).CatalogPage })}><Route path="new" lazy={async () => ({ Component: (await import('../contributions/Pages')).NewContributionRoute })} /></Route>
      <Route path="models" element={<LegacyRedirect from="/admin/models" to="/admin/catalog" />} />
      <Route path="suppliers" element={<LegacyRedirect from="/admin/suppliers" to="/admin/catalog" />} />
      <Route lazy={async()=>({Component:(await import('../group-channels/Pages')).AdminGroupChannelsPage})} path="channels" />
      <Route
        lazy={async () => ({ Component: (await import('../group-channels/Pages')).AdminGroupChannelPage })}
        path="channels/:id/*"
      />
      <Route lazy={async () => ({ Component: (await import('./TradesPage')).AdminTradesPage })} path="trades" />
      <Route
        lazy={async () => ({ Component: (await import('./TradesPage')).AdminTradeDetailPage })}
        path="trades/:tradeID"
      />
      <Route lazy={async () => ({ Component: (await import('./SettingsPage')).SettingsPage })} path="settings" />
 <Route lazy={async()=>({Component:(await import('../detection/SearchPage')).DetectionSearchPage})} path="settings/detection" />
 <Route lazy={async()=>({Component:(await import("../typical/Page")).TypicalCostsPage})} path="settings/typical-costs" />
    </Route>
  </Route>
)
