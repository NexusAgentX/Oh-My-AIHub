import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react'
import { useBeforeUnload, useBlocker } from 'react-router-dom'
import { ConfirmDialog } from './Dialog'

type Guard = { dirty: boolean; busy: boolean; scope?: string; bypass: boolean; skip?: RefObject<boolean> }
const Registry = createContext<{ register: (guard: { current: Guard }) => () => void; changed: () => void } | null>(null)

/** 一个路由阻断器汇总各表单，避免嵌套编辑时多个 blocker 相互覆盖。 */
export function UnsavedGuardProvider({ children }: { children: ReactNode }) {
  const entries = useRef(new Set<{ current: Guard }>())
  const [guards, setGuards] = useState<Guard[]>([])
  const changed = useCallback(() => setGuards([...entries.current].map(entry => ({ ...entry.current }))), [])
  const register = useCallback((guard: { current: Guard }) => { entries.current.add(guard); changed(); return () => { entries.current.delete(guard); changed() } }, [changed])
  const registry = useMemo(() => ({ register, changed }), [register, changed])
  const blocker = useBlocker(({ nextLocation }) => [...entries.current].some(({ current: guard }) =>
    !guard.bypass && !guard.skip?.current && (guard.dirty || guard.busy) && (!guard.scope || !(nextLocation.pathname === guard.scope || nextLocation.pathname.startsWith(guard.scope + '/'))),
  ))
  useBeforeUnload(useCallback((event: BeforeUnloadEvent) => {
    if ([...entries.current].some(({ current: guard }) => !guard.bypass && !guard.skip?.current && (guard.dirty || guard.busy))) { event.preventDefault(); event.returnValue = '' }
  }, []))
  const busy = guards.some(entry => entry.busy)
  return <Registry.Provider value={registry}>{children}<ConfirmDialog open={blocker.state === 'blocked'} title="放弃未保存的修改？" description="离开后，未保存的修改将丢失。" confirmLabel="放弃并离开" busy={busy} onClose={() => blocker.reset?.()} onConfirm={() => blocker.proceed?.()} /></Registry.Provider>
}

export function useUnsavedGuard(dirty: boolean, { scope, busy = false, skip }: { scope?: string; busy?: boolean; skip?: RefObject<boolean> } = {}) {
  const registry = useContext(Registry)
  const guard = useRef<Guard>({ dirty, busy, scope, skip, bypass: false })
  useEffect(() => { Object.assign(guard.current, { dirty, busy, scope, skip }); registry?.changed() }, [dirty, busy, scope, skip, registry])
  useEffect(() => registry?.register(guard), [registry])
  return useCallback(() => { guard.current.bypass = true }, [])
}
