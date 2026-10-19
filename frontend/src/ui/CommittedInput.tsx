import { useEffect, useState, type InputHTMLAttributes } from 'react'

/** 文本和数字筛选保留输入草稿，回车或失焦才应用。 */
export function CommittedInput({ value, onCommit, ...props }: Omit<InputHTMLAttributes<HTMLInputElement>, 'value' | 'onChange'> & {
  value: string
  onCommit: (value: string) => void
}) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  const commit = () => { if (draft !== value) onCommit(draft) }
  return <>
    <input {...props} value={draft} onChange={event => setDraft(event.target.value)} onBlur={commit} onKeyDown={event => {
      if (event.key === 'Enter') { event.preventDefault(); commit() }
      if (event.key === 'Escape') { event.preventDefault(); setDraft(value) }
    }} />
    {draft !== value && <small className="filter-pending" role="status">待应用</small>}
  </>
}
