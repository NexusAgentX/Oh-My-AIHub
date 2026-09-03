import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api/client'
import type { GatewayCall } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState } from '../ui/FormControls'
import { CallTable } from './CallTable'

export function CallsPage() {
  const [calls, setCalls] = useState<GatewayCall[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setCalls(await api.gatewayCalls(100))
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '调用记录加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <AppShell>
      <header className="page-heading"><div><h1>调用记录</h1></div><Button disabled={loading} onClick={() => void load()} type="button" variant="secondary">刷新</Button></header>
      <InlineError>{error}</InlineError>
      <section className="panel table-panel">
        <header className="table-toolbar"><h2>最近 100 笔</h2><span className="count-badge">{calls.length}</span></header>
        {loading && calls.length === 0 ? <LoadingState /> : <CallTable calls={calls} />}
      </section>
    </AppShell>
  )
}
