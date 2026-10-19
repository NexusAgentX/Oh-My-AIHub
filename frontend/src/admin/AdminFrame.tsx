import { AppFrame } from '../layouts/AppFrame'
import { adminGroups } from './navigation'

/** 治理路由保留独立权限与懒加载，共用外壳的焦点、顶栏和移动端行为。 */
export function AdminFrame() { return <AppFrame navigation={adminGroups} /> }
