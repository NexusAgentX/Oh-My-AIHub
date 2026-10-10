import { useEffect } from 'react'
import { ButtonLink, Icon } from '../ui'
import { AuthShell } from './AuthShell'

/** 未匹配的地址：登录与否都显示这一页；“回到首页”交给 `/` 按会话决定去产品首页还是落地页。 */
export function NotFoundPage() {
  useEffect(() => {
    const previousTitle = document.title
    document.title = '找不到页面 · Oh-My-AIHub'
    return () => {
      document.title = previousTitle
    }
  }, [])

  return (
    <AuthShell description="地址可能写错了，或者页面已经移走。" mascot="oh" title="哦！这个页面不存在">
      <div className="identity-form">
        <ButtonLink icon={<Icon name="home" />} to="/" variant="primary">
          回到首页
        </ButtonLink>
      </div>
    </AuthShell>
  )
}
