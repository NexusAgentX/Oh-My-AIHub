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

  it('states positioning, value, fallback and boundaries', () => {
    expect(markup).toContain('<main')
    expect(markup).toContain('<footer')
    expect(markup).toContain('API 渠道市场')
    expect(markup).toContain('积分 C2C')
    expect(markup).toContain('消费者')
    expect(markup).toContain('共享者')
    expect(markup).toContain('备用顺序')
    expect(markup).toContain('不保存请求与响应正文')
    expect(markup).toContain('不承诺兑付')
  })

  it('offers only the invited login path as the account action', () => {
    expect(markup.match(/href="\/login"/g)?.length).toBeGreaterThanOrEqual(2)
    expect(markup).toContain('受邀用户登录')
    expect(markup).toContain('暂不开放自由注册')
    expect(markup).not.toContain('href="/register"')
    expect(markup).not.toContain('找回密码')
    expect(markup).not.toContain('免费注册')
  })

  it('does not introduce composite recommendations or fabricated social proof', () => {
    expect(markup).not.toContain('综合推荐')
    expect(markup).not.toContain('客户数量')
    expect(markup).not.toContain('用户增长')
    expect(markup).not.toContain('月收入')
    expect(markup).not.toContain('99.9%')
  })
})
