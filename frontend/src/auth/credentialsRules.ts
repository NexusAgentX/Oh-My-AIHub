export const usernameRuleText = '3-32 位，小写字母、数字、.、_ 或 -，以小写字母或数字开头'
export const passwordRuleText = '至少 8 位，包含大写字母、小写字母和数字'

const usernamePattern = /^[a-z0-9][a-z0-9._-]{2,31}$/

export function usernameProblem(username: string): string | null {
  return usernamePattern.test(username) ? null : usernameRuleText
}

export function passwordProblem(password: string): string | null {
  const valid =
    password.length >= 8 &&
    password.length <= 128 &&
    /[A-Z]/.test(password) &&
    /[a-z]/.test(password) &&
    /[0-9]/.test(password)
  return valid ? null : passwordRuleText
}
