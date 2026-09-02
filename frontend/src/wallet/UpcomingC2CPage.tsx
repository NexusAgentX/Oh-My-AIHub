import { Link } from 'react-router-dom'
import { AppShell } from '../layouts/AppShell'

export function UpcomingC2CPage() {
  return (
    <AppShell>
      <section className="panel upcoming-panel">
        <span className="count-badge">即将启用</span>
        <h1>C2C 积分市场</h1>
        <p>当前入口已预留，买单、卖单和订单管理将在市场能力启用后开放。</p>
        <Link className="button button-secondary" to="/wallet">返回积分钱包</Link>
      </section>
    </AppShell>
  )
}
