export const createdCredentialPath = '/admin/accounts/created'

export function shouldClearCredential(previousPath: string, nextPath: string) {
  return previousPath === createdCredentialPath && nextPath !== createdCredentialPath
}
