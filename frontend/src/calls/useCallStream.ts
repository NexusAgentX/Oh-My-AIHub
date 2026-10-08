import { useCallback, useEffect, useRef, useState } from 'react'

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
 * url 为 null 时关闭。连接失败（含接口尚未实现）时不自动重连，状态变为 error，由用户重试。
 */
export function useCallStream<T extends { id: string }>(url: string | null) {
  const [items, setItems] = useState<T[]>([])
  const [status, setStatus] = useState<StreamStatus>('off')
  const [attempt, setAttempt] = useState(0)
  const source = useRef<EventSource | null>(null)

  useEffect(() => {
    if (!url || typeof EventSource === 'undefined') {
      setStatus('off')
      return
    }
    setStatus('connecting')
    const events = new EventSource(url, { withCredentials: true })
    source.current = events
    events.onopen = () => setStatus('open')
    events.onmessage = (event: MessageEvent<string>) => {
      try {
        const item = JSON.parse(event.data) as T
        if (item && typeof item.id === 'string') setItems((current) => mergeStreamItem(current, item))
      } catch {
        // 非 JSON 的心跳等消息直接忽略
      }
    }
    events.onerror = () => {
      events.close()
      setStatus('error')
    }
    return () => {
      events.close()
      source.current = null
    }
  }, [url, attempt])

  const retry = useCallback(() => setAttempt((value) => value + 1), [])
  const clear = useCallback(() => setItems([]), [])
  return { items, status, retry, clear }
}
