import type { components, operations } from './schema.gen'

// API 类型的唯一来源是由 backend/api/openapi.yaml 生成的 schema.gen.ts。
// 此文件只提供简洁别名，不重新描述任何 JSON 结构。

type Schemas = components['schemas']

/** 某个 operationId 的 2xx JSON 响应体。 */
export type ResponseBody<Op extends keyof operations> = operations[Op] extends {
  responses: infer R
}
  ? {
      [S in keyof R]: S extends 200 | 201
        ? R[S] extends { content: { 'application/json': infer B } }
          ? B
          : never
        : never
    }[keyof R]
  : never

/** 某个 operationId 的 JSON 请求体。 */
export type RequestBody<Op extends keyof operations> = operations[Op] extends {
  requestBody: { content: { 'application/json': infer B } }
}
  ? B
  : never

export type Account = Schemas['Account']
export type AccountStatus = Account['status']
export type Points = Schemas['Points']
