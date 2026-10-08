import { Navigate, Outlet, Route } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { AdminFrame } from './AdminFrame'
import { CallsPage } from './CallsPage'
import { ChannelDetailPage, ChannelsPage } from './ChannelsPage'
import { DisputeDetailPage, DisputesPage } from './DisputesPage'
import { ModelsPage } from './ModelsPage'
import { OverviewPage } from './OverviewPage'
import { PointsPage } from './PointsPage'
import { SettingsPage } from './SettingsPage'
import { UsersPage } from './UsersPage'

/** 非管理员访问 /admin/** 时回到产品首页；后台入口只在管理员的账户菜单中出现。 */
function RequireAdmin() {
  const { account } = useAuth()
  if (!account?.is_admin) return <Navigate replace to="/home" />
  return <Outlet />
}

/** 管理后台路由，挂在 App.tsx 已登录且完成首次改密的分支下。 */
export const adminRoutes = (
  <Route element={<RequireAdmin />}>
    <Route element={<AdminFrame />} path="/admin">
      <Route element={<OverviewPage />} index />
      <Route element={<CallsPage />} path="calls" />
      <Route element={<PointsPage />} path="points" />
      <Route element={<UsersPage />} path="users" />
      <Route element={<ModelsPage />} path="models" />
      <Route element={<ChannelsPage />} path="channels" />
      <Route element={<ChannelDetailPage />} path="channels/:channelID" />
      <Route element={<DisputesPage />} path="disputes" />
      <Route element={<DisputeDetailPage />} path="disputes/:tradeID" />
      <Route element={<SettingsPage />} path="settings" />
    </Route>
  </Route>
)
