import DOMPurify from 'dompurify'
import hljs from 'highlight.js/lib/common'

export interface MarkdownUpload {
  url: string
  name: string
  isImage: boolean
}

export function isAttachmentURL(url: string) {
  return /^\/api\/forum\/attachments\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(url)
}

export function uploadMarkdown(file: MarkdownUpload) {
  if (!isAttachmentURL(file.url)) throw new Error('上传结果不是有效的站内附件地址')
  const name = file.name.replace(/[\r\n\u0000-\u001f\u007f]/g, ' ').replace(/[\\`*_[\]<>!]/g, '\\$&')
  return `${file.isImage ? '!' : ''}[${name}](${file.url})`
}

export function sanitizeMarkdownHTML(html: string) {
  const fragment = DOMPurify.sanitize(html, {
    RETURN_DOM_FRAGMENT: true,
    ALLOWED_TAGS: ['p', 'br', 'hr', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'strong', 'em', 'del', 's', 'blockquote', 'ul', 'ol', 'li', 'a', 'img', 'pre', 'code', 'table', 'thead', 'tbody', 'tr', 'th', 'td', 'input', 'sup'],
    ALLOWED_ATTR: ['href', 'src', 'alt', 'title', 'class', 'start', 'type', 'checked', 'disabled'],
    ALLOW_DATA_ATTR: false,
    ALLOW_ARIA_ATTR: false,
  })
  fragment.querySelectorAll('img').forEach((image) => {
    if (!isAttachmentURL(image.getAttribute('src') || '')) image.replaceWith(document.createTextNode(image.alt || '[图片不可用]'))
    else { image.loading = 'lazy'; image.decoding = 'async' }
  })
  fragment.querySelectorAll('a').forEach((link) => {
    const href = link.getAttribute('href') || ''
    if (!/^(https?:\/\/|mailto:)/i.test(href) && !isAttachmentURL(href) && !/^#[\w-]+$/.test(href)) link.removeAttribute('href')
    link.rel = 'noopener noreferrer nofollow'
  })
  fragment.querySelectorAll('input').forEach((input) => {
    if (input.type !== 'checkbox') input.remove()
    else { input.disabled = true; input.setAttribute('aria-label', input.checked ? '已完成' : '未完成') }
  })
  fragment.querySelectorAll('[class]').forEach((element) => {
    const language = element.tagName === 'CODE' ? element.className.match(/(?:^|\s)language-([a-zA-Z0-9_+-]+)(?:\s|$)/)?.[1] : undefined
    element.removeAttribute('class')
    if (language && element.parentElement?.tagName === 'PRE' && hljs.getLanguage(language)) {
      element.innerHTML = hljs.highlight(element.textContent || '', { language, ignoreIllegals: true }).value
      element.className = 'hljs'
    }
  })
  const container = document.createElement('div')
  container.append(fragment)
  return container.innerHTML
}

let parser: Lute | undefined
export function renderMarkdown(value: string) {
  if (!parser) {
    parser = Lute.New()
    parser.SetSanitize(true)
    parser.SetHeadingAnchor(false)
    parser.SetHeadingID(false)
    parser.SetToC(false)
    parser.SetFootnotes(false)
    parser.SetCallout(false)
    parser.SetVditorCodeBlockPreview(false)
    parser.SetVditorMathBlockPreview(false)
    parser.PutEmojis({})
    parser.SetEmojiSite('')
    const omitHTML: ILuteRenderCallback = () => ['', Lute.WalkSkipChildren]
    parser.SetJSRenderers({ renderers: { Md2HTML: { renderHTML: omitHTML, renderInlineHTML: omitHTML } } })
  }
  return sanitizeMarkdownHTML(parser.Md2HTML(value))
}
