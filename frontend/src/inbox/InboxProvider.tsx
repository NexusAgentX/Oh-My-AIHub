import { createContext, useContext, useEffect, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useAuth } from '../auth/AuthProvider'
import { request } from '../api/client'
import { usePageVisible } from '../api/visibility'
import type { Inbox } from './model'

function useInboxQuery() {
  const { account } = useAuth(), visible = usePageVisible(), client = useQueryClient()
  const query = useQuery({ queryKey: ['inbox', account?.id ?? '', account?.is_admin ?? false], enabled: Boolean(account && !account.must_change_password),
    queryFn: ({ signal }) => request<Inbox>('/api/inbox', { signal }), staleTime: 30_000, refetchOnWindowFocus: true,
    refetchInterval: query => visible ? query.state.data?.sources.some(source => source.items.some(item => item.deadline)) ? 30_000 : 60_000 : false,
  })
  const nearest = query.data?.sources.flatMap(source => source.items).map(item => item.deadline).filter((value): value is string => Boolean(value)).sort((a, b) => new Date(a).getTime() - new Date(b).getTime())[0]
  useEffect(() => {
    if (!visible || !nearest) return
    const timer = setTimeout(() => { void client.invalidateQueries({ queryKey: ['inbox'] }) }, Math.max(0, new Date(nearest).getTime() - Date.now()))
    return () => clearTimeout(timer)
  }, [nearest, visible, client])
  return query
}
const InboxContext = createContext<ReturnType<typeof useInboxQuery> | null>(null)
export function InboxProvider({ children }: { children: ReactNode }) {
  const query = useInboxQuery()
  return <InboxContext.Provider value={query}>{children}</InboxContext.Provider>
}
export function useInbox() {
  const value = useContext(InboxContext)
  if (!value) throw new Error('InboxProvider is required')
  return value
}
