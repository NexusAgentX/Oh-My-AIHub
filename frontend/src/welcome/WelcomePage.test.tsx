import { renderToStaticMarkup } from 'react-dom/server'
import { matchRoutes } from 'react-router-dom'
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
  const markup = renderToStaticMarkup(<WelcomePage />)

  it('renders the confirmed product story without a global topbar', () => {
    expect(markup).not.toContain('welcome-topbar')
    expect(markup).toContain('<main id="welcome-main"')
    expect(markup).toContain('<footer')
    expect(markup).toContain('把分散的 API 渠道，')
    expect(markup).toContain('API 消费者')
    expect(markup).toContain('渠道共享者')
    expect(markup).toContain('顺序故障回退')
    expect(markup).toContain('不保存请求与响应正文')
    expect(markup).toContain('买单和卖单都能部分成交')
  })

  it('offers only the invited login path as the account action', () => {
    expect(markup.match(/href="\/login"/g)?.length).toBeGreaterThanOrEqual(4)
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
  })
})
