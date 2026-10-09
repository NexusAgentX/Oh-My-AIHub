import { useRef, useState, type ReactNode } from 'react'
import { errorMessage } from '../api/query'
import { MarkdownEditor } from '../markdown'
import { Button, InlineError } from '../ui'
import { Attachments } from './components'
import { contentError, type Attachment } from './model'
import { forumApi } from './queries'

/** Mount with a key for the exact draft/content context, isolating late uploads. */
export function ContentComposer({ initialBody = '', initialAttachments = [], onSave, onCancel, submitLabel, children, validate }: {
  initialBody?: string; initialAttachments?: Attachment[]; onSave: (body: string, attachmentIDs: string[]) => Promise<unknown>;
  onCancel?: () => void; submitLabel: string; children?: ReactNode; validate?: () => string;
}) {
  const [body, setBody] = useState(initialBody)
  const [attachments, setAttachments] = useState(initialAttachments)
  const currentAttachments = useRef(attachments)
  currentAttachments.current = attachments
  const [uploading, setUploading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const upload = async (file: File) => {
    if (file.size > 10 * 1048576) throw new Error('单文件不能超过 10 MiB')
    if (currentAttachments.current.length >= 20) throw new Error('每篇内容最多 20 个附件')
    const attachment = await forumApi.upload(file)
    currentAttachments.current = [...currentAttachments.current, attachment]
    setAttachments(currentAttachments.current)
    return { url: attachment.url, name: attachment.name, isImage: attachment.inline }
  }
  return <form className="forum-composer" onSubmit={(event) => {
    event.preventDefault()
    if (saving || uploading) return
    const message = validate?.() || contentError(body, attachments)
    if (message) { setError(message); return }
    setError(''); setSaving(true)
    void onSave(body, attachments.map((item) => item.id)).catch((cause) => setError(errorMessage(cause, '保存失败，请重试'))).finally(() => setSaving(false))
  }}>
    <fieldset disabled={saving}>
      {children}
      <MarkdownEditor value={body} onChange={setBody} upload={upload} onUploadingChange={setUploading} disabled={saving} />
      <Attachments items={attachments} disabled={saving || uploading} onRemove={(id) => {
        // Keep source editable: the user may intentionally retain a filename or code example.
        setAttachments((items) => items.filter((item) => item.id !== id))
      }} />
      <p className="forum-upload-hint">单文件最多 10 MiB，每篇最多 20 个。移除附件并保存后，原文件链接失效。</p>
      <InlineError>{error}</InlineError>
      <div className="forum-form-actions">
        <Button type="submit" loading={saving} disabled={uploading}>{submitLabel}</Button>
        {onCancel && <Button type="button" variant="secondary" disabled={saving || uploading} onClick={onCancel}>取消</Button>}
      </div>
    </fieldset>
  </form>
}
