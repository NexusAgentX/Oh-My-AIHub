import { Navigate, useLocation } from 'react-router-dom'

/** 旧入口保留对象后缀、筛选、锚点与来源；只保留重定向，不保留兼容页面。 */
export function LegacyRedirect({ from, to }: { from: string; to: string }) {
  const location = useLocation()
  return <Navigate replace to={`${to}${location.pathname.slice(from.length)}${location.search}${location.hash}`} state={location.state} />
}
