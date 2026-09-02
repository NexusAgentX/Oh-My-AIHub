import { describe, expect, it } from 'vitest'
import {
  createdCredentialPath,
  shouldClearCredential,
} from './ephemeralCredential'

describe('one-time credential route lifecycle', () => {
  it('clears credentials as soon as the user leaves the reveal page', () => {
    expect(shouldClearCredential(createdCredentialPath, '/admin/accounts')).toBe(
      true,
    )
    expect(shouldClearCredential(createdCredentialPath, '/account')).toBe(true)
  })

  it('does not clear during the initial transition into the reveal page', () => {
    expect(shouldClearCredential('/admin/accounts', createdCredentialPath)).toBe(
      false,
    )
    expect(
      shouldClearCredential(createdCredentialPath, createdCredentialPath),
    ).toBe(false)
  })
})
