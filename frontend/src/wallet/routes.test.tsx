import type { ReactElement } from 'react'
import { matchRoutes, Navigate } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { appRoutes } from '../App'

describe('ledger routes', () => {
  it.each([
    ['/wallet', '/wallet'],
    ['/admin/ops', '/admin/ops'],
    ['/admin/ledger/accounts/00000000-0000-0000-0000-000000000001', '/admin/ledger/accounts/:accountID'],
  ])('matches %s', (pathname, expectedRoute) => {
    expect(matchRoutes(appRoutes, pathname)?.at(-1)?.route.path).toBe(expectedRoute)
  })

  it('redirects the retired insufficient-balance page into the wallet', () => {
    const route = matchRoutes(appRoutes, '/wallet/insufficient')?.at(-1)?.route
    expect(route?.path).toBe('/wallet/insufficient')
    const element = route?.element as ReactElement<{ to: string; replace?: boolean }>
    expect(element.type).toBe(Navigate)
    expect(element.props).toMatchObject({ to: '/wallet', replace: true })
  })

  it.each([
    ['/c2c', '/c2c'],
    ['/c2c/orders/new?side=buy', '/c2c/orders/new'],
    ['/c2c/me', '/c2c/me'],
  ])('keeps the recovery entry %s out of the wildcard redirect', (pathname, expectedRoute) => {
    expect(matchRoutes(appRoutes, pathname)?.at(-1)?.route.path).toBe(expectedRoute)
  })
})
