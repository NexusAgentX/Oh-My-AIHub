import { matchRoutes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { appRoutes } from '../App'
import { addOfferTarget, keySettingsTarget, newKeyTarget } from './redirects'

describe('gateway routes', () => {
  it.each([
    ['/dashboard', '/dashboard'],
    ['/keys', '/keys'],
    ['/keys/key-id', '/keys/:keyID'],
    ['/calls', '/calls'],
    ['/calls/call-id', '/calls/:callID'],
    ['/market', '/market'],
    ['/market/channels/channel-id', '/market/channels/:channelID'],
  ])('matches %s', (pathname, expectedRoute) => {
    expect(matchRoutes(appRoutes, pathname)?.at(-1)?.route.path).toBe(expectedRoute)
  })

  it('keeps replaced full-page entries reachable as redirects into drawers', () => {
    expect(matchRoutes(appRoutes, '/keys/new')?.at(-1)?.route.path).toBe('/keys/new')
    expect(matchRoutes(appRoutes, '/keys/k/settings')?.at(-1)?.route.path).toBe('/keys/:keyID/settings')
    expect(matchRoutes(appRoutes, '/market/channels/c/add')?.at(-1)?.route.path).toBe(
      '/market/channels/:channelID/add',
    )
    expect(newKeyTarget).toBe('/keys?new=1')
    expect(keySettingsTarget('k 1')).toBe('/keys/k%201?settings=1')
    expect(addOfferTarget('c1', 'o1')).toBe('/market/channels/c1?add=o1')
    expect(addOfferTarget('c1', null)).toBe('/market/channels/c1?add=1')
  })
})
