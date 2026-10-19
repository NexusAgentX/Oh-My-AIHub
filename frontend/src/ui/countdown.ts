import { useEffect, useState } from 'react'
import { usePageVisible } from '../api/visibility'

/** 付款倒计时（秒），到期后为 0。 */
export function useCountdown(deadline?: string | null) {
  const [now, setNow] = useState(() => Date.now())
  const visible = usePageVisible()
  const end = deadline ? new Date(deadline).getTime() : 0
  useEffect(() => {
    if (!visible || !end) return
    setNow(Date.now())
    if (end <= Date.now()) return
    const timer = window.setInterval(() => {
      const time = Date.now()
      setNow(time)
      if (time >= end) window.clearInterval(timer)
    }, 1000)
    return () => window.clearInterval(timer)
  }, [visible, end])
  return Math.max(0, (end - now) / 1000)
}

