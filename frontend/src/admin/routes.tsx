import { Navigate, Outlet, Route } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'

/** 非管理员访问 /admin/** 时回到产品首页；后台入口只在管理员的账户菜单中出现。 */
function RequireAdmin() {
  const { account } = useAuth()
  if (!account?.is_admin) return <Navigate replace to="/home" />
  return <Outlet />
}

/** 管理后台路由，挂在 App.tsx 已登录且完成首次改密的分支下；页面按需加载，普通用户不下载后台代码。 */
export const adminRoutes = (
  <Route element={<RequireAdmin />}>
    <Route lazy={async () => ({ Component: (await import('./AdminFrame')).AdminFrame })} path="/admin">
      <Route index lazy={async () => ({ Component: (await import('./OverviewPage')).OverviewPage })} />
      <Route lazy={async () => ({ Component: (await import('./CallsPage')).CallsPage })} path="calls" />
      <Route lazy={async () => ({ Component: (await import('./PointsPage')).PointsPage })} path="points" />
      <Route lazy={async () => ({ Component: (await import('./UsersPage')).UsersPage })} path="users" />
      <Route lazy={async () => ({ Component: (await import('./ModelsPage')).ModelsPage })} path="models" />
      <Route lazy={async () => ({ Component: (await import('./ChannelsPage')).ChannelsPage })} path="channels" />
      <Route
        lazy={async () => ({ Component: (await import('./ChannelsPage')).ChannelDetailPage })}
        path="channels/:channelID"
      />
      <Route lazy={async () => ({ Component: (await import('./DisputesPage')).DisputesPage })} path="disputes" />
      <Route
        lazy={async () => ({ Component: (await import('./DisputesPage')).DisputeDetailPage })}
        path="disputes/:tradeID"
      />
      <Route lazy={async () => ({ Component: (await import('./SettingsPage')).SettingsPage })} path="settings" />
    </Route>
  </Route>
)
