import { AppFrame } from './AppFrame'
import { productNavigation } from './navigation'

/** 用户界面 layout route：页面作为 Outlet 子路由渲染，不再自行包裹外壳。 */
export function ProductLayout() {
  return <AppFrame navigation={productNavigation} variant="product" />
}
