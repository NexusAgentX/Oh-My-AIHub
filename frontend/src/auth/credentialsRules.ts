export const usernameRuleText = '3-32 位，小写字母、数字、.、_ 或 -，以小写字母或数字开头'
export const passwordRuleText = '至少 12 个字符'

const usernamePattern = /^[a-z0-9][a-z0-9._-]{2,31}$/

export function usernameProblem(username: string): string | null {
  return usernamePattern.test(username) ? null : usernameRuleText
}

export function passwordProblem(password: string): string | null {
  if (password.length >= 12 && password.length <= 128) return null
  return passwordRuleText
}
