import { useEffect, useState } from 'react'
import { loadMarkdownRuntime } from './runtime'
import { renderMarkdown } from './render'
import './markdown.css'

export function MarkdownContent({ value, className = '' }: { value: string; className?: string }) {
  const [rendered, setRendered] = useState<{ value: string; html: string }>()
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    loadMarkdownRuntime().then(() => {
      if (active) { setRendered({ value, html: renderMarkdown(value) }); setError('') }
    }).catch(() => { if (active) setError('内容加载失败，请刷新重试') })
    return () => { active = false }
  }, [value])
  if (error) return <p role="alert">{error}</p>
  // Do not retain another post/ticket's HTML while its replacement loads.
  return <div className={`markdown-content ${className}`} aria-busy={rendered?.value !== value}
    dangerouslySetInnerHTML={{ __html: rendered?.value === value ? rendered.html : '' }} />
}
