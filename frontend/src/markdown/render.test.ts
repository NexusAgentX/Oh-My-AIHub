// @vitest-environment jsdom
import luteSource from 'vditor/dist/js/lute/lute.min.js?raw'
import { beforeAll, describe, expect, it } from 'vitest'
import { isAttachmentURL, renderMarkdown, sanitizeMarkdownHTML, uploadMarkdown } from './render'

const url = '/api/forum/attachments/11111111-1111-4111-8111-111111111111'
beforeAll(() => {
  window.eval(luteSource)
})

describe('shared Markdown rendering', () => {
  it('renders GFM tables, task lists and highlighted code through the real Lute engine', () => {
    const html = renderMarkdown('# Heading\n\n| A | B |\n| - | - |\n| a | b |\n\n- [x] Done\n\n```js\nconst x = "ok"\n```')
    expect(html).toContain('<h1>Heading</h1>')
    expect(html).toContain('<table>')
    expect(html).toContain('type="checkbox"')
    expect(html).toContain('disabled')
    expect(html).toContain('hljs-keyword')
  })
  it('omits raw HTML while keeping fenced code literal', () => {
    expect(renderMarkdown('<b>raw HTML</b>\n\nText <i>inline</i>')).not.toMatch(/<(b|i)>/)
    expect(renderMarkdown('```html\n<b>literal</b>\n```')).toContain('&lt;')
  })
  it('permits only exact protected attachment URLs for images', () => {
    expect(renderMarkdown(`![local](${url})`)).toContain(`src="${url}"`)
    expect(renderMarkdown('![remote](https://example.invalid/image.png)')).not.toContain('<img')
    expect(isAttachmentURL(`${url}?redirect=https://example.invalid`)).toBe(false)
    expect(isAttachmentURL(`https://example.invalid${url}`)).toBe(false)
  })
  it('allows ordinary https links and strips active content, attributes and diagram hooks', () => {
    const html = sanitizeMarkdownHTML('<p style="color:red" onclick="void 0">text</p><iframe></iframe><a href="javascript:void(0)">bad</a><a href="https://example.com">site</a><code class="language-mermaid">flowchart LR</code>')
    expect(html).not.toMatch(/onclick|style=|iframe|javascript:|language-mermaid/)
    expect(html).toContain('href="https://example.com"')
    expect(html).toContain('noopener noreferrer nofollow')
  })
  it('escapes uploaded filenames without creating extra Markdown nodes', () => {
    const name = 'a](*title*)<img>.txt'
    const html = renderMarkdown(uploadMarkdown({ url, name, isImage: false }))
    const element = document.createElement('div')
    element.innerHTML = html
    expect(element.querySelectorAll('a')).toHaveLength(1)
    expect(element.querySelector('a')?.textContent).toBe(name)
    expect(element.querySelector('img')).toBeNull()
    expect(() => uploadMarkdown({ url: '//external.invalid/image', name, isImage: true })).toThrow()
  })
})
