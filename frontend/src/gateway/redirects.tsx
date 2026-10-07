import { Navigate, useParams, useSearchParams } from 'react-router-dom'

/** 旧整页入口被抽屉替代后的新位置；纯函数便于测试。 */
export const newKeyTarget = '/keys?new=1'

export function keySettingsTarget(keyID: string) {
  return `/keys/${encodeURIComponent(keyID)}?settings=1`
}

export function addOfferTarget(channelID: string, offerID: string | null) {
  const query = offerID ? encodeURIComponent(offerID) : '1'
  return `/market/channels/${encodeURIComponent(channelID)}?add=${query}`
}

/** 旧「新建 Key」整页入口：改为列表页中的抽屉。 */
export function RedirectNewKey() {
  return <Navigate replace to={newKeyTarget} />
}

/** 旧「Key 配置」整页入口：改为 Key 详情页中的设置抽屉。 */
export function RedirectKeySettings() {
  const { keyID = '' } = useParams()
  return <Navigate replace to={keySettingsTarget(keyID)} />
}

/** 旧「加入模型协议池」整页入口：改为渠道详情页中的加入路由抽屉。 */
export function RedirectAddOffer() {
  const { channelID = '' } = useParams()
  const [search] = useSearchParams()
  return <Navigate replace to={addOfferTarget(channelID, search.get('offer'))} />
}
