import { AppFrame } from './AppFrame'
import { adminNavigation } from './navigation'

/** 管理后台 layout route：独立导航，并提供返回产品界面的入口。 */
export function AdminLayout() {
  return <AppFrame navigation={adminNavigation} variant="admin" />
}
