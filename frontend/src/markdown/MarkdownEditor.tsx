import { useEffect, useId, useRef, useState } from 'react'
import Vditor from 'vditor'
import 'vditor/dist/index.css'
import { MarkdownContent } from './MarkdownContent'
import { loadMarkdownRuntime } from './runtime'
import { uploadMarkdown, type MarkdownUpload } from './render'
import './markdown.css'

export interface MarkdownEditorProps {
  value: string
  onChange: (value: string) => void
  upload?: (file: File) => Promise<MarkdownUpload>
  onUploadingChange?: (uploading: boolean) => void
  disabled?: boolean
  label?: string
  placeholder?: string
}

export function MarkdownEditor(props: MarkdownEditorProps) {
  const { value, disabled = false, label = '正文', placeholder = '使用 Markdown 编写内容' } = props
  const id = useId()
  const host = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)
  const editor = useRef<Vditor | null>(null)
  const latest = useRef(props)
  latest.current = props
  const lifetime = useRef(0)
  const pending = useRef(false)
  const [ready, setReady] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [error, setError] = useState('')
  const [showPreview, setShowPreview] = useState(true)

  async function uploadFiles(files: File[]) {
    if (!latest.current.upload || latest.current.disabled || pending.current || !editor.current || !files.length) return
    const generation = lifetime.current
    const callback = latest.current.upload
    pending.current = true
    setUploading(true)
    setError('')
    latest.current.onUploadingChange?.(true)
    try {
      for (const file of files) {
        const result = await callback(file)
        if (lifetime.current !== generation) return
        editor.current?.insertValue(`\n${uploadMarkdown(result)}\n`)
      }
    } catch (cause) {
      if (lifetime.current === generation) setError(cause instanceof Error ? cause.message : '上传失败，请重试')
    } finally {
      if (lifetime.current === generation) {
        pending.current = false
        setUploading(false)
        latest.current.onUploadingChange?.(false)
      }
    }
  }
  const uploadRef = useRef(uploadFiles)
  uploadRef.current = uploadFiles

  useEffect(() => {
    const generation = ++lifetime.current
    let instance: Vditor | undefined
    let initialized = false
    const container = host.current!
    // Capture clipboard HTML before Vditor's HTML conversion. Only plain text
    // and uploaded files enter this source editor; remote clipboard images stay inert.
    const paste = (event: ClipboardEvent) => {
      if (!(event.target instanceof HTMLTextAreaElement)) return
      event.preventDefault()
      event.stopPropagation()
      if (latest.current.disabled || !initialized) return
      const files = Array.from(event.clipboardData?.files || [])
      if (files.length) void uploadRef.current(files)
      else instance?.insertValue(event.clipboardData?.getData('text/plain') || '')
    }
    const drop = (event: DragEvent) => {
      event.preventDefault()
      event.stopPropagation()
      if (!latest.current.disabled) void uploadRef.current(Array.from(event.dataTransfer?.files || []))
    }
    container.addEventListener('paste', paste, true)
    container.addEventListener('drop', drop, true)
    loadMarkdownRuntime().then(() => {
      if (generation !== lifetime.current) return
      instance = new Vditor(container, {
        mode: 'sv', lang: 'zh_CN', i18n: window.VditorI18n,
        cdn: '/assets', cache: { enable: false },
        value: latest.current.value, placeholder: latest.current.placeholder || '使用 Markdown 编写内容',
        minHeight: 260, toolbarConfig: { pin: false },
        toolbar: ['headings', 'bold', 'italic', 'strike', '|', 'list', 'ordered-list', 'check', 'quote', '|', 'link', 'code', 'inline-code', 'table', '|', 'undo', 'redo'],
        hint: { parse: false, emoji: {}, emojiPath: '' },
        preview: { mode: 'editor', actions: [], hljs: { enable: false }, theme: { current: '', path: '' }, markdown: { codeBlockPreview: false, mathBlockPreview: false, sanitize: true }, render: { media: { enable: false } } },
        input: (next) => latest.current.onChange(next),
        after: () => {
          // Vditor's final initialization runs in a microtask after construction.
          initialized = true
          // Icons were loaded with a src URL; avoid Vditor's inline-script loader.
          instance!.vditor.options.icon = undefined
          if (generation !== lifetime.current) { instance?.destroy(); return }
          editor.current = instance!
          const textarea = container.querySelector('textarea.vditor-sv')!
          textarea.id = `${id}-source`
          textarea.setAttribute('aria-describedby', `${id}-status`)
          if (latest.current.disabled) instance!.disabled()
          if (instance!.getValue() !== latest.current.value) instance!.setValue(latest.current.value)
          setReady(true)
        },
      })
    }).catch((cause) => { if (generation === lifetime.current) setError(cause instanceof Error ? cause.message : '编辑器加载失败，请刷新重试') })
    return () => {
      // lifetime 是代际计数器而非 DOM ref：清理时必须读取并递增最新值，使进行中的异步回调失效。
      // oxlint-disable-next-line react/exhaustive-deps
      ++lifetime.current
      container.removeEventListener('paste', paste, true)
      container.removeEventListener('drop', drop, true)
      editor.current = null
      if (initialized) instance?.destroy()
      if (pending.current) { pending.current = false; latest.current.onUploadingChange?.(false) }
    }
  }, [id])

  useEffect(() => {
    if (ready && editor.current && value !== editor.current.getValue()) editor.current.setValue(value)
  }, [value, ready])
  useEffect(() => {
    if (ready) { if (disabled) editor.current?.disabled(); else editor.current?.enable() }
  }, [disabled, ready])

  return <div className="markdown-editor" aria-busy={!ready || uploading}>
    <div className="markdown-editor-actions">
      <label htmlFor={`${id}-source`}>{label}</label>
      <div>
        {props.upload && <button type="button" disabled={disabled || !ready || uploading} onClick={() => input.current?.click()}>上传图片或附件</button>}
        <button type="button" aria-pressed={showPreview} aria-controls={`${id}-preview`} onClick={() => setShowPreview(!showPreview)}>{showPreview ? '隐藏预览' : '显示预览'}</button>
      </div>
    </div>
    <input ref={input} type="file" multiple hidden disabled={disabled || uploading || !ready} aria-label="上传图片或附件"
      onChange={(event) => { const files = Array.from(event.target.files || []); event.target.value = ''; void uploadFiles(files) }} />
    <div className={`markdown-editor-panes${showPreview ? ' markdown-editor-panes--split' : ''}`}>
      <div ref={host} className="markdown-editor-source" inert={disabled} data-placeholder={placeholder} />
      {showPreview && <section id={`${id}-preview`} className="markdown-editor-preview" aria-label="Markdown 预览"><MarkdownContent value={value} /></section>}
    </div>
    <p id={`${id}-status`} role="status" className="markdown-editor-status">{uploading ? '正在上传，请稍候…' : !ready ? '正在加载编辑器…' : ''}</p>
    {error && <p role="alert" className="markdown-editor-error">{error}</p>}
  </div>
}
