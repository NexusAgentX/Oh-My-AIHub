import { useMutation } from '@tanstack/react-query'
import { api } from '../api/client'
import { useAuth } from './AuthProvider'
import { useInstance } from './InstanceProvider'

/** 登录：成功后由 AuthProvider 写入会话，调用方负责跳转。 */
export function useLoginMutation() {
  const { login } = useAuth()
  return useMutation({
    mutationFn: (input: { username: string; password: string }) =>
      login(input.username, input.password),
  })
}

/** 修改密码（首次改密与账户设置共用）。 */
export function useChangePasswordMutation() {
  const { changePassword } = useAuth()
  return useMutation({
    mutationFn: (input: { currentPassword: string; newPassword: string }) =>
      changePassword(input.currentPassword, input.newPassword),
  })
}

/** 初始化实例并刷新实例与会话状态，返回应进入的账户。 */
export function useInitializeInstanceMutation() {
  const { refresh } = useInstance()
  const { refresh: refreshSession } = useAuth()
  return useMutation({
    mutationFn: async (input: {
      username: string
      displayName: string
      password: string
    }) => {
      const result = await api.initializeInstance(
        input.username,
        input.displayName,
        input.password,
      )
      await refresh()
      return (await refreshSession()) ?? result.account
    },
  })
}
