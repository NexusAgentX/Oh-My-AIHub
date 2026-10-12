import { useEffect } from 'react'
import { Button, ButtonLink, Icon } from '../ui'
import { AuthShell } from './AuthShell'

export function RouteErrorPage() {
  useEffect(() => {
    const previousTitle = document.title
    document.title = '页面出错了 · Oh-My-AIHub'
    return () => {
      document.title = previousTitle
    }
  }, [])

  return (
    <AuthShell description="页面暂时无法显示，请刷新重试。" mascot="oh" title="哦！页面出错了">
      <div className="identity-form">
        <Button onClick={() => window.location.reload()} type="button">刷新页面</Button>
        <ButtonLink icon={<Icon name="home" />} reloadDocument to="/">回到首页</ButtonLink>
      </div>
    </AuthShell>
  )
}
