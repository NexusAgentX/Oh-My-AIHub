// 只保留最近一次创建的明文；不进入路由状态、持久存储或查询/变更缓存。
let pending: { id: string; secret: string } | undefined
export function handoffKeySecret(id: string, secret: string) { pending = { id, secret } }
export function takeKeySecret(id: string) { const secret = pending?.id === id ? pending.secret : ''; pending = undefined; return secret }
export function clearKeySecret() { pending = undefined }
