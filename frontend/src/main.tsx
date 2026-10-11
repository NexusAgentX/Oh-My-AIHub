import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import './styles/index.css'
import { startTheme } from './theme/theme'

startTheme()

// 发版后旧页面引用的分块已不存在：刷新一次加载新版本，短时间内重复失败则交给错误页。
window.addEventListener('vite:preloadError', (event) => {
  const key = 'aihub:chunk-reload-at'
  try {
    const last = Number(sessionStorage.getItem(key) ?? 0)
    if (Date.now() - last < 10_000) return
    sessionStorage.setItem(key, String(Date.now()))
  } catch {
    return
  }
  event.preventDefault()
  window.location.reload()
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
