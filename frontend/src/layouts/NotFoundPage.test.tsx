import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { NotFoundPage } from './NotFoundPage'

describe('NotFoundPage', () => {
  it('shows the surprised mascot, a short explanation and a way home', () => {
    const markup = renderToStaticMarkup(
      <MemoryRouter initialEntries={['/nowhere']}>
        <NotFoundPage />
      </MemoryRouter>,
    )
    expect(markup).toContain('<h1>哦！这个页面不存在</h1>')
    expect(markup).toContain('class="mascot ')
    expect(markup).toMatch(/<a[^>]*href="\/"[^>]*>.*回到首页/)
  })
})
