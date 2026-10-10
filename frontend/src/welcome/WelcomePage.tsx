import { useEffect } from 'react'
import { Link, Navigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { ButtonLink, Icon, Mascot, type IconName } from '../ui'
import { BrandMark } from '../layouts/Brand'

/** tone 按含义取色：用 API 为信息蓝，卖 API 是收入绿，积分是芥末。 */
const markets: Array<{ icon: IconName; tone: 'sky' | 'mint' | 'mustard'; title: string; body: string }> = [
  {
    icon: 'key',
    tone: 'sky',
    title: '用 API',
    body: '一个地址、一把 Key 用多个中转站；按价格、稳定或速度自动选，失败自动换下一个。',
  },
  {
    icon: 'server',
    tone: 'mint',
    title: '卖 API',
    body: '把已充值的中转站共享出来，按倍率赚积分；上游 Key 加密保存，不对外显示。',
  },
  {
    icon: 'coins',
    tone: 'mustard',
    title: '积分',
    body: '信用额度起步；用户之间站外人民币买卖积分。',
  },
]

const boundaries: Array<{ title: string; body: string }> = [
  { title: '隐私', body: '不保存请求与响应正文。' },
  { title: '人民币', body: '平台不经手、不托管人民币；1 积分 ≈ 1 元仅作参考，不承诺兑付。' },
]

export function WelcomePage() {
  useEffect(() => {
    const previousTitle = document.title
    document.title = 'Oh My AIHub · API 市场 + 积分市场'
    return () => {
      document.title = previousTitle
    }
  }, [])

  return (
    <div className="welcome">
      <a className="skip-link" href="#welcome-main">
        跳到主要内容
      </a>
      <header className="welcome-header">
        <Link aria-label="Oh-My-AIHub 首页" to="/">
          <BrandMark size={36} />
        </Link>
        <ButtonLink size="sm" to="/login" variant="secondary">
          登录
        </ButtonLink>
      </header>

      <main className="welcome-main" id="welcome-main">
        <section aria-labelledby="welcome-title" className="welcome-hero">
          <div className="welcome-hero-copy">
            <p className="welcome-lockup">
              Oh-My-<span>AIHub</span>
            </p>
            <p className="welcome-eyebrow">API 市场 + 积分市场</p>
            <h1 id="welcome-title">用大家的中转站，也把你的共享出去</h1>
            <div className="welcome-actions">
              <ButtonLink icon={<Icon name="chevron-right" />} to="/login" variant="primary">
                登录
              </ButtonLink>
              <span className="welcome-invite">
                <Icon name="shield" />
                受邀制，账号由管理员创建
              </span>
            </div>
          </div>
          <Mascot bounce kind="drop" size={132} />
        </section>

        <ul aria-label="能做什么" className="welcome-values">
          {markets.map((item) => (
            <li key={item.title}>
              <span className={`welcome-value-icon welcome-value-${item.tone}`}>
                <Icon name={item.icon} />
              </span>
              <strong>{item.title}</strong>
              <p>{item.body}</p>
            </li>
          ))}
        </ul>

        <section aria-label="边界" className="welcome-boundaries">
          {boundaries.map((item) => (
            <p key={item.title}>
              <strong>{item.title}</strong>
              {item.body}
            </p>
          ))}
        </section>
      </main>

      <footer className="welcome-footer">
        <span>© Oh My AIHub</span>
        <span>仅限受邀成员</span>
      </footer>
    </div>
  )
}

/** 公开入口 `/`：已登录用户直接进入产品首页，其余看到落地页。 */
export function LandingRoute() {
  const { account } = useAuth()
  if (account) return <Navigate replace to="/home" />
  return <WelcomePage />
}
