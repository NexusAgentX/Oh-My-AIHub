import { matchRoutes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { appRoutes } from '../App'

describe('channel and market routes', () => {
  it.each([
    ['/market', '/market'],
    ['/market/channels/channel-id', '/market/channels/:channelID'],
    ['/channels', '/channels'],
    ['/channels/new', '/channels/new'],
    ['/channels/channel-id', '/channels/:channelID'],
    ['/channels/channel-id/settings', '/channels/:channelID/settings'],
    ['/admin/channels', '/admin/channels'],
    ['/admin/channels/channel-id', '/admin/channels/:channelID'],
  ])('matches %s', (pathname, expectedRoute) => {
    expect(matchRoutes(appRoutes, pathname)?.at(-1)?.route.path).toBe(expectedRoute)
  })
})
