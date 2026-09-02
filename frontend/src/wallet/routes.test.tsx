import { matchRoutes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { appRoutes } from '../App'

describe('ledger routes', () => {
  it.each([
    ['/wallet', '/wallet'],
    ['/wallet/insufficient', '/wallet/insufficient'],
    ['/admin/ops', '/admin/ops'],
    ['/admin/ledger/accounts/00000000-0000-0000-0000-000000000001', '/admin/ledger/accounts/:accountID'],
  ])('matches %s', (pathname, expectedRoute) => {
    expect(matchRoutes(appRoutes, pathname)?.at(-1)?.route.path).toBe(expectedRoute)
  })

  it.each([
    ['/c2c', '/c2c'],
    ['/c2c/orders/new?side=buy', '/c2c/orders/new'],
    ['/c2c/me', '/c2c/me'],
  ])('keeps the recovery entry %s out of the wildcard redirect', (pathname, expectedRoute) => {
    expect(matchRoutes(appRoutes, pathname)?.at(-1)?.route.path).toBe(expectedRoute)
  })
})
