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
  ])('matches %s to route %s', (pathname, expectedRoute) => {
    expect(matchRoutes(appRoutes, pathname)?.at(-1)?.route.path).toBe(expectedRoute)
  })

  it('未匹配路径落到落地页通配路由', () => {
    expect(matchRoutes(appRoutes, '/not-a-page')?.at(-1)?.route.path).toBe('*')
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
