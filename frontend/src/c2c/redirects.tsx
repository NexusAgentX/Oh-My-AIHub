import { Navigate, useParams } from 'react-router-dom'

/** 旧「承接挂单」页并入市场页抽屉。 */
export function TakeOrderRedirect() {
  const { orderID = '' } = useParams()
  return <Navigate replace to={`/c2c?take=${encodeURIComponent(orderID)}`} />
}

/** 旧「争议」页并入交易详情页对话框。 */
export function DisputeRedirect() {
  const { tradeID = '' } = useParams()
  return <Navigate replace to={`/c2c/trades/${encodeURIComponent(tradeID)}`} />
}
