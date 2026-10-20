import { matchRoutes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { appRoutes } from '../App'

describe('instance routes', () => {
  it.each([
    ['/', '/'],
    ['/', '/'],
    ['/initialize', '/initialize'],
    ['/welcome', '/welcome'],
    ['/home', '/home'],
    ['/models', '/models'],
    ['/models/gpt-5', '/models/:model'],
    ['/keys', '/keys'],
    ['/usage', '/usage'],
    ['/channels', '/channels'],
    ['/channels/new', '/channels/new'],
    ['/channels/abc', '/channels/:id'],
    ['/points', '/points'],
    ['/points/trades/abc', '/points/trades/:id'],
    ['/account', '/account'],
    ['/me', '/me'],
  ])('matches %s to route %s', (pathname, expectedRoute) => {
    expect(matchRoutes(appRoutes, pathname)?.at(-1)?.route.path).toBe(expectedRoute)
  })

  it.each(['/not-a-page', '/admin/not-a-page', '/models/gpt-5/extra'])('未匹配路径 %s 落到 404 页', (pathname) => {
    const last = matchRoutes(appRoutes, pathname)?.at(-1)?.route
    expect(last?.path).toBe('*')
    expect((last?.element as { type?: { name?: string } } | undefined)?.type?.name).toBe('NotFoundPage')
  })

  it('初始化路由独立于会话门卫，控制台路由仍受会话门卫保护', () => {
    const homeMatch = matchRoutes(appRoutes, '/home') ?? []
    const homeGuards = homeMatch.map((m) => (typeof m.route.element === 'object' && m.route.element !== null && 'type' in m.route.element ? String((m.route.element as { type?: { name?: string } }).type?.name) : ''))
    expect(homeGuards).toContain('RequireSession')
    const initializeMatch = matchRoutes(appRoutes, '/initialize') ?? []
    const initializeGuards = initializeMatch.map((m) => (typeof m.route.element === 'object' && m.route.element !== null && 'type' in m.route.element ? String((m.route.element as { type?: { name?: string } }).type?.name) : ''))
    expect(initializeGuards).not.toContain('RequireSession')
  })
})
