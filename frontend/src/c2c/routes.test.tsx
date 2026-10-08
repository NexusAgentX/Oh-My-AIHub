import type { ReactElement } from 'react'
import { matchRoutes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { appRoutes } from '../App'
import { DisputeRedirect, TakeOrderRedirect } from './redirects'

describe('C2C routes', () => {
  it.each([
    ['/c2c', '/c2c'],
    ['/c2c/orders/new', '/c2c/orders/new'],
    ['/c2c/orders/order-id/take', '/c2c/orders/:orderID/take'],
    ['/c2c/me', '/c2c/me'],
    ['/c2c/trades/trade-id', '/c2c/trades/:tradeID'],
    ['/c2c/trades/trade-id/dispute', '/c2c/trades/:tradeID/dispute'],
    ['/admin/c2c/disputes', '/admin/c2c/disputes'],
    ['/admin/c2c/disputes/trade-id', '/admin/c2c/disputes/:tradeID'],
  ])('matches %s', (pathname, expectedRoute) => {
    expect(matchRoutes(appRoutes, pathname)?.at(-1)?.route.path).toBe(expectedRoute)
  })

  it('keeps the retired take and dispute pages as redirects into the market drawer and trade dialog', () => {
    const element = (pathname: string) =>
      matchRoutes(appRoutes, pathname)?.at(-1)?.route.element as ReactElement
    expect(element('/c2c/orders/order-id/take').type).toBe(TakeOrderRedirect)
    expect(element('/c2c/trades/trade-id/dispute').type).toBe(DisputeRedirect)
  })
})
