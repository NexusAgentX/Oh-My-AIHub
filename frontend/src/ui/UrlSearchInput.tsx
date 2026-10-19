import { useEffect, useRef, useState } from 'react'
import { SearchInput } from './Form'

/** Searches commit to URL after a short pause; Back and deep links reset the draft. */
export function UrlSearchInput({ value, onCommit, label, placeholder }: { value: string; onCommit: (value: string) => void; label: string; placeholder?: string }) {
  const [draft, setDraft] = useState(value), commit = useRef(onCommit)
  commit.current = onCommit
  useEffect(() => setDraft(value), [value])
  useEffect(() => { if (draft === value) return; const timer = setTimeout(() => commit.current(draft.trim()), 300); return () => clearTimeout(timer) }, [draft, value])
  return <SearchInput label={label} placeholder={placeholder} value={draft} onChange={event => setDraft(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); onCommit(draft.trim()) } }} />
}
