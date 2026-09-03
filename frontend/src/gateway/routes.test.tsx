import { matchRoutes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { appRoutes } from '../App'

describe('gateway routes', () => {
  it.each([
    ['/dashboard', '/dashboard'],
    ['/keys', '/keys'],
    ['/keys/new', '/keys/new'],
    ['/keys/key-id', '/keys/:keyID'],
    ['/keys/key-id/settings', '/keys/:keyID/settings'],
    ['/calls', '/calls'],
    ['/calls/call-id', '/calls/:callID'],
  ])('matches %s', (pathname, expectedRoute) => {
    expect(matchRoutes(appRoutes, pathname)?.at(-1)?.route.path).toBe(
      expectedRoute,
    )
  })
})
