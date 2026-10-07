import { useEffect } from 'react'
import { Link } from 'react-router-dom'
import { ButtonLink, Icon, type IconName } from '../ui'
import { Brand } from '../layouts/Brand'

const pillars: Array<{ icon: IconName; title: string; body: string }> = [
  {
    icon: 'store',
    title: 'API 渠道市场',
    body: '共享者提交渠道，消费者按价格、成功率与响应速度挑选。',
  },
  {
    icon: 'coins',
    title: '积分 C2C',
    body: 'API 收入以积分结算，用户之间挂单买卖，人民币在平台外直接支付。',
  },
]

const values: Array<{ icon: IconName; title: string; body: string }> = [
  {
    icon: 'key',
    title: '消费者',
    body: '一把平台 Key 访问多个模型，自己决定每条路由的渠道。',
  },
  {
    icon: 'server',
    title: '共享者',
    body: '把已充值的渠道分享给小圈子，按模型设置倍率，收入单独可查。',
  },
  {
    icon: 'layers',
    title: '备用顺序',
    body: '从最高优先级开始，输出开始前失败才依次尝试下一渠道。',
  },
]

const boundaries: Array<{ title: string; body: string }> = [
  { title: '隐私', body: '不保存请求与响应正文；上游 Key 加密保存，写入后不可回显。' },
  {
    title: '积分',
    body: '积分只用于平台内计价与清算，1 积分 ≈ 1 元仅为参考单位，不承诺兑付。',
  },
]

export function WelcomePage() {
  useEffect(() => {
    const previousTitle = document.title
    document.title = 'Oh My AIHub · API 共享平台'
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
          <Brand />
        </Link>
        <ButtonLink size="sm" to="/login" variant="secondary">
          受邀用户登录
        </ButtonLink>
      </header>

      <main className="welcome-main" id="welcome-main">
        <section aria-labelledby="welcome-title" className="welcome-hero">
          <p className="welcome-eyebrow">熟人小圈子的 API 共享平台</p>
          <h1 id="welcome-title">把分散的 API 渠道，变成一个可靠入口</h1>
          <p className="welcome-lead">
            一个平台 Key 组合多个模型与渠道，价格与质量清楚可见，失败时按备用顺序回退。
          </p>
          <div className="welcome-actions">
            <ButtonLink icon={<Icon name="chevron-right" />} to="/login" variant="primary">
              受邀用户登录
            </ButtonLink>
            <span className="welcome-invite">
              <Icon name="shield" />
              账号由管理员创建，暂不开放自由注册
            </span>
          </div>
        </section>

        <ul aria-label="产品定位" className="welcome-pillars">
          {pillars.map((item) => (
            <li key={item.title}>
              <Icon name={item.icon} size={20} />
              <div>
                <strong>{item.title}</strong>
                <p>{item.body}</p>
              </div>
            </li>
          ))}
        </ul>

        <ul aria-label="使用价值" className="welcome-values">
          {values.map((item) => (
            <li key={item.title}>
              <span className="welcome-value-icon">
                <Icon name={item.icon} />
              </span>
              <strong>{item.title}</strong>
              <p>{item.body}</p>
            </li>
          ))}
        </ul>

        <section aria-label="隐私与积分边界" className="welcome-boundaries">
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
