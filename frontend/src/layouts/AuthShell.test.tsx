import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { AuthShell } from './AuthShell'

const render = (props: Parameters<typeof AuthShell>[0]) =>
  renderToStaticMarkup(
    <MemoryRouter>
      <AuthShell {...props} />
    </MemoryRouter>,
  )

describe('AuthShell', () => {
  it('renders brand link, badge, heading and description', () => {
    const markup = render({ badge: '首次登录', description: '说明', title: '设置', children: <i>body</i> })
    expect(markup).toContain('href="/"')
    expect(markup).toContain('首次登录')
    expect(markup).toContain('<h1>设置</h1>')
    expect(markup).toContain('说明')
    expect(markup).toContain('<i>body</i>')
  })

  it('omits the heading when no title is given (loading state)', () => {
    expect(render({ children: null })).not.toContain('<h1')
  })
})
