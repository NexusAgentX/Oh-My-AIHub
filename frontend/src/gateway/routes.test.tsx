import { matchRoutes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { appRoutes } from '../App'

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

})
