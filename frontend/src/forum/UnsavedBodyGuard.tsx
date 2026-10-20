import { useBeforeUnload, useBlocker } from 'react-router-dom'
import { useCallback, type RefObject } from 'react'
import { ConfirmDialog } from '../ui'

export function UnsavedBodyGuard({ dirty, discard }: { dirty: boolean; discard: RefObject<boolean> }) {
  const blocker = useBlocker(() => dirty && !discard.current)
  useBeforeUnload(useCallback((event: BeforeUnloadEvent) => {
    if (dirty) { event.preventDefault(); event.returnValue = '' }
  }, [dirty]))
  return <ConfirmDialog open={blocker.state === 'blocked'} title="丢弃未保存的正文？" description="离开后，未提交的修改将丢失。"
    confirmLabel="丢弃并离开" onClose={() => blocker.reset?.()} onConfirm={() => blocker.proceed?.()} />
}
