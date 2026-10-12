import { useEffect } from 'react'
import { isRouteErrorResponse, useRouteError } from 'react-router-dom'
import { Button, ButtonLink, CopyButton, Icon } from '../ui'
import { AuthShell } from './AuthShell'

export function RouteErrorPage() {
  const error = useRouteError()
  const errorDetails = error instanceof Error
    ? error.stack || `${error.name}: ${error.message}`
    : isRouteErrorResponse(error)
      ? `${error.status} ${error.statusText}\n${typeof error.data === 'string' ? error.data : JSON.stringify(error.data, null, 2)}`
      : String(error ?? '未知错误')

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
        <CopyButton label="复制错误信息" size="md" value={errorDetails} variant="quiet" />
      </div>
    </AuthShell>
  )
}
