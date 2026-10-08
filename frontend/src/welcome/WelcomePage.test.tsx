import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter, matchRoutes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { appRoutes } from '../App'
import { WelcomePage } from './WelcomePage'

describe('welcome route', () => {
  it.each(['/welcome', '/welcome/'])(
    'matches %s as the public welcome route',
    (pathname) => {
      const matches = matchRoutes(appRoutes, pathname)
      expect(matches?.at(-1)?.route.path).toBe('/welcome')
    },
  )
})

describe('WelcomePage', () => {
  const markup = renderToStaticMarkup(
    <MemoryRouter>
      <WelcomePage />
    </MemoryRouter>,
  )

  it('states the API market + points market positioning and boundaries', () => {
    expect(markup).toContain('<main')
    expect(markup).toContain('<footer')
    expect(markup).toContain('API 市场 + 积分市场')
    expect(markup).toContain('用 API')
    expect(markup).toContain('卖 API')
    expect(markup).toContain('积分')
    expect(markup).toContain('失败自动换')
    expect(markup).toContain('Key 加密保存')
    expect(markup).toContain('不保存请求与响应正文')
    expect(markup).toContain('不托管人民币')
  })

  it('offers only login as the account action', () => {
    expect(markup.match(/href="\/login"/g)?.length).toBeGreaterThanOrEqual(2)
    expect(markup).toContain('受邀制')
    expect(markup).not.toContain('href="/register"')
    expect(markup).not.toContain('注册')
  })

  it('does not introduce fabricated social proof', () => {
    expect(markup).not.toContain('客户数量')
    expect(markup).not.toContain('用户增长')
    expect(markup).not.toContain('99.9%')
  })
})
