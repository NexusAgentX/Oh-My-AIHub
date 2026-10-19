import { useCallback, useEffect, useRef, useState } from 'react'
import { usePageVisible } from '../api/visibility'

export type StreamStatus = 'off' | 'connecting' | 'open' | 'error'

/** 把新到达的一条合并进列表：同 ID 替换（例如进行中 → 完成），否则插入顶部。 */
export function mergeStreamItem<T extends { id: string }>(items: T[], item: T, max = 200) {
  const index = items.findIndex((existing) => existing.id === item.id)
  if (index >= 0) {
    const next = items.slice()
    next[index] = item
    return next
  }
  return [item, ...items].slice(0, max)
}

/**
 * 订阅调用实时流（SSE，每个 event 的 data 为一条 JSON）。
 * 隐藏页面断开；失败最多自动重连三次，随后由用户重试。
 */
export function useCallStream<T extends { id: string }>(url: string | null) {
  const [items, setItems] = useState<T[]>([])
  const [status, setStatus] = useState<StreamStatus>('off')
  const [attempt, setAttempt] = useState(0)
  const source = useRef<EventSource | null>(null)
  const visible = usePageVisible()

  useEffect(() => {
    setItems([])
    if (!url || !visible || typeof EventSource === 'undefined') {
      setStatus('off')
      return
    }
    let stopped = false
    let retries = 0
    let timer: ReturnType<typeof setTimeout> | undefined
    const connect = () => {
      if (stopped) return
      setStatus('connecting')
      const events = new EventSource(url, { withCredentials: true })
      source.current = events
      events.onopen = () => { if (!stopped) setStatus('open') }
      const receive = (event: MessageEvent<string>) => {
        if (stopped) return
        try {
          const item = JSON.parse(event.data) as T
          if (item && typeof item.id === 'string') setItems(current => mergeStreamItem(current, item))
        } catch {
          // 心跳等非 JSON 消息不进入列表。
        }
      }
      events.onmessage = receive
      events.addEventListener('call.started', receive as EventListener)
      events.addEventListener('call.finished', receive as EventListener)
      events.onerror = () => {
        events.close()
        if (stopped) return
        if (retries < 3) {
          setStatus('connecting')
          timer = setTimeout(connect, 1000 * 2 ** retries++)
        } else setStatus('error')
      }
    }
    connect()
    return () => {
      stopped = true
      clearTimeout(timer)
      source.current?.close()
      source.current = null
    }
  }, [url, attempt, visible])

  const retry = useCallback(() => setAttempt((value) => value + 1), [])
  const clear = useCallback(() => setItems([]), [])
  return { items, status, retry, clear }
}
