import { useEffect, useState } from 'react'
import { usePageVisible } from '../api/visibility'
import { shanghaiDate } from '../money/time'

/** 相对查看范围按记账日历推进；隐藏页面停止计时。 */
export function useShanghaiDay() {
  const visible = usePageVisible()
  const [day, setDay] = useState(() => shanghaiDate(new Date()))
  useEffect(() => {
    if (!visible) return
    const refresh = () => setDay(shanghaiDate(new Date()))
    refresh()
    const timer = window.setInterval(refresh, 60_000)
    window.addEventListener('focus', refresh)
    return () => { window.clearInterval(timer); window.removeEventListener('focus', refresh) }
  }, [visible])
  return day
}
