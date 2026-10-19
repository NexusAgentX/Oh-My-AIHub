import type { RefObject } from 'react'
import { useUnsavedGuard } from '../ui/UnsavedGuard'

export function UnsavedBodyGuard({ dirty, discard }: { dirty: boolean; discard: RefObject<boolean> }) {
  useUnsavedGuard(dirty, { skip: discard })
  return null
}
