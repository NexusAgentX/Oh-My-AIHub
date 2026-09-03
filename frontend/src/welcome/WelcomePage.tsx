import { useEffect, type ReactNode } from 'react'
import './WelcomePage.css'

type IconName =
  | 'account'
  | 'arrow'
  | 'balance'
  | 'check'
  | 'copy'
  | 'database'
  | 'eye-off'
  | 'hub'
  | 'key'
  | 'link'
  | 'lock'
  | 'route'
  | 'share'
  | 'terminal'
  | 'tune'

type IconProps = {
  name: IconName
  size?: number
}

function Icon({ name, size = 20 }: IconProps) {
  const paths: Record<IconName, ReactNode> = {
    account: (
      <>
        <path d="M4 19h16" />
        <path d="M6 16V8l6-3 6 3v8" />
        <path d="M9 10v4M12 10v4M15 10v4" />
      </>
    ),
    arrow: <path d="M5 12h14m-5-5 5 5-5 5" />,
    balance: (
      <>
        <path d="M4 7h16M6 7v10m12-10v10M3 20h18" />
        <path d="m8 4 4-2 4 2" />
      </>
    ),
    check: <path d="m5 12 4 4L19 6" />,
    copy: (
      <>
        <rect x="9" y="9" width="10" height="10" rx="2" />
        <path d="M15 9V7a2 2 0 0 0-2-2H7a2 2 0 0 0-2 2v6a2 2 0 0 0 2 2h2" />
      </>
    ),
    database: (
      <>
        <ellipse cx="12" cy="5" rx="7" ry="3" />
        <path d="M5 5v7c0 1.7 3.1 3 7 3s7-1.3 7-3V5M5 12v7c0 1.7 3.1 3 7 3s7-1.3 7-3v-7" />
      </>
    ),
    'eye-off': (
      <>
        <path d="m3 3 18 18" />
        <path d="M10.6 10.7a2 2 0 0 0 2.7 2.7" />
        <path d="M9.9 4.2A10.5 10.5 0 0 1 12 4c5.5 0 9 6 9 6a15 15 0 0 1-2.2 2.9M6.2 6.2C3.9 7.7 3 10 3 10s3.5 6 9 6a9.8 9.8 0 0 0 3.7-.7" />
      </>
    ),
    hub: (
      <>
        <circle cx="12" cy="12" r="2.5" />
        <circle cx="5" cy="5" r="1.5" />
        <circle cx="19" cy="5" r="1.5" />
        <circle cx="12" cy="20" r="1.5" />
        <path d="m7 7 3.2 3.2M17 7l-3.2 3.2M12 14.5V18" />
      </>
    ),
    key: (
      <>
        <circle cx="8" cy="15" r="4" />
        <path d="m11 12 8-8m-3 3 2 2m-5 1 2 2" />
      </>
    ),
    link: (
      <>
        <path d="m10 13 4-4" />
        <path d="M7.5 15.5 5 18a4 4 0 0 1-6-6l3-3a4 4 0 0 1 5.7-.3" transform="translate(3 -1)" />
        <path d="m16.5 8.5 2.5-2.5a4 4 0 1 0-6-6l-3 3a4 4 0 0 0-.3 5.7" transform="translate(0 3)" />
      </>
    ),
    lock: (
      <>
        <rect x="5" y="10" width="14" height="10" rx="2" />
        <path d="M8 10V7a4 4 0 0 1 8 0v3" />
      </>
    ),
    route: (
      <>
        <circle cx="6" cy="6" r="2" />
        <circle cx="18" cy="6" r="2" />
        <circle cx="6" cy="18" r="2" />
        <circle cx="18" cy="18" r="2" />
        <path d="M8 6h8M6 8v8M8 18h8" />
      </>
    ),
    share: (
      <>
        <circle cx="18" cy="5" r="2" />
        <circle cx="6" cy="12" r="2" />
        <circle cx="18" cy="19" r="2" />
        <path d="m8 11 8-5m-8 7 8 5" />
      </>
    ),
    terminal: (
      <>
        <path d="m5 7 4 5-4 5M12 17h7" />
      </>
    ),
    tune: (
      <>
        <path d="M4 6h6m4 0h6M4 12h2m4 0h10M4 18h10m4 0h2" />
        <circle cx="12" cy="6" r="2" />
        <circle cx="8" cy="12" r="2" />
        <circle cx="16" cy="18" r="2" />
      </>
    ),
  }

  return (
    <svg
      aria-hidden="true"
      className="welcome-icon"
      fill="none"
      height={size}
      viewBox="0 0 24 24"
      width={size}
    >
      <g stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.8">
        {paths[name]}
      </g>
    </svg>
  )
}

function Brand() {
  return (
    <span className="welcome-brand">
      <span className="welcome-brand-mark">
        <Icon name="hub" size={18} />
      </span>
      <span>Oh My AIHub</span>
    </span>
  )
}

function LoginLink({ children, className = '' }: { children: ReactNode; className?: string }) {
  return (
    <a className={`welcome-button welcome-button-primary ${className}`.trim()} href="/login">
      <span>{children}</span>
      <Icon name="arrow" size={17} />
    </a>
  )
}

const proofItems: Array<{ icon: IconName; title: string; detail: string }> = [
  { icon: 'key', title: '一把平台 Key', detail: '按 Key 独立配置模型协议池' },
  { icon: 'hub', title: '4 种原生格式', detail: '不做协议间转换' },
  { icon: 'route', title: '顺序故障回退', detail: '从最高优先级开始' },
  { icon: 'eye-off', title: '0 请求正文留存', detail: '只记录指标与错误' },
]

const marketRows = [
  {
    model: '通用推理模型',
    provider: 'Campus Relay',
    multiplier: '1.00×',
    ttft: '0.8s',
    tps: '86',
    availability: '99.9%',
    score: '4.9',
    featured: true,
  },
  {
    model: '通用推理模型',
    provider: 'Harbor API',
    multiplier: '0.94×',
    ttft: '1.2s',
    tps: '74',
    availability: '99.6%',
    score: '4.7',
  },
  {
    model: '多模态模型',
    provider: 'North Bridge',
    multiplier: '1.08×',
    ttft: '0.9s',
    tps: '62',
    availability: '99.8%',
    score: '4.8',
  },
  {
    model: '长上下文模型',
    provider: 'Lake Route',
    multiplier: '0.89×',
    ttft: '1.6s',
    tps: '55',
    availability: '99.2%',
    score: '4.6',
  },
]

const workflowSteps: Array<{ number: string; icon: IconName; title: string; body: string; signal: string }> = [
  {
    number: '01',
    icon: 'link',
    title: '连接渠道',
    body: '共享者提交 Base URL 与上游 Key，再从公开模型目录选择能力。',
    signal: '下一步',
  },
  {
    number: '02',
    icon: 'tune',
    title: '组合模型协议池',
    body: '消费者为平台 Key 选择模型，并为每个模型加入候选渠道。',
    signal: '下一步',
  },
  {
    number: '03',
    icon: 'route',
    title: '顺序回退',
    body: '每次从最高优先级开始，请求失败才继续尝试下一渠道。',
    signal: '下一步',
  },
  {
    number: '04',
    icon: 'balance',
    title: '透明清算',
    body: '调用同时记消费者支出与共享者收入，手续费进入明确账户。',
    signal: '零和记账',
  },
]

const traceSteps: Array<{ icon: IconName; title: string; meta: string; state: string; success?: boolean }> = [
  { icon: 'key', title: '平台接收请求', meta: 'API Key 已授权该模型', state: '通过' },
  { icon: 'route', title: '优先级 1 · Campus Relay', meta: '上游超时 · 已记录错误', state: '回退' },
  {
    icon: 'check',
    title: '优先级 2 · Harbor API',
    meta: 'TTFT 与 TPS 已计入统计',
    state: '成功',
    success: true,
  },
  { icon: 'balance', title: '双边账本同步入账', meta: '消费者支出 ↔ 共享者收入', state: '平衡' },
]

const c2cSteps: Array<{ icon: IconName; title: string; body: string }> = [
  { icon: 'account', title: '01 · 发布挂单', body: '选择买单或卖单、数量、价格与支付方式' },
  { icon: 'lock', title: '02 · 冻结积分', body: '卖单立即冻结可成交积分，避免重复支出' },
  { icon: 'check', title: '03 · 确认后放行', body: '买家声明付款，卖家确认到账后平台划转积分' },
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
    <div className="welcome-page">
      <a className="welcome-skip-link" href="#welcome-main">
        跳到主要内容
      </a>

      <main id="welcome-main">
        <section aria-labelledby="welcome-hero-title" className="welcome-hero">
          <div className="welcome-hero-copy">
            <p className="welcome-pill">
              <span aria-hidden="true" className="welcome-dot" />
              熟人小圈子的 API 共享平台
            </p>
            <h1 className="welcome-hero-title" id="welcome-hero-title">
              把分散的 API 渠道，<br />
              变成一个可靠入口
            </h1>
            <p className="welcome-hero-body">
              一个平台 Key，组合多个模型与渠道。价格、性能和稳定性清楚可见，失败时按优先级自动回退。
            </p>
            <div className="welcome-hero-actions">
              <LoginLink>受邀用户登录</LoginLink>
              <a className="welcome-button welcome-button-secondary" href="#workflow">
                <span>了解工作方式</span>
                <Icon name="arrow" size={17} />
              </a>
            </div>
            <p className="welcome-invite-note">
              <Icon name="lock" size={17} />
              账号由管理员创建并交付登录凭据
            </p>
          </div>

          <div aria-label="平台 API 模型协议池界面示意" className="welcome-gateway-frame">
            <div className="welcome-gateway-panel">
              <div className="welcome-gateway-header">
                <div>
                  <span className="welcome-demo-eyebrow">PLATFORM API</span>
                  <strong>Production Gateway</strong>
                </div>
                <span className="welcome-status">
                  <span aria-hidden="true" className="welcome-status-dot" />
                  运行中
                </span>
              </div>
              <div className="welcome-demo-divider" />
              <div className="welcome-model-card">
                <span className="welcome-dark-icon">
                  <Icon name="hub" />
                </span>
                <span className="welcome-model-copy">
                  <strong>通用推理模型</strong>
                  <small>Responses API · 2 个渠道</small>
                </span>
                <span className="welcome-route-pill">
                  <Icon name="route" size={16} />
                  顺序回退
                </span>
              </div>
              <div className="welcome-channel-heading">
                <strong>渠道优先级</strong>
                <span>实时质量指标</span>
              </div>
              <ol className="welcome-channel-list">
                <li>
                  <span className="welcome-priority">1</span>
                  <span>
                    <strong>Campus Relay</strong>
                    <small>TTFT 0.8s · TPS 86</small>
                  </span>
                  <span className="welcome-channel-metric">
                    <strong>1.00×</strong>
                    <small><span aria-hidden="true" className="welcome-status-dot" /> 99.9%</small>
                  </span>
                </li>
                <li>
                  <span className="welcome-priority">2</span>
                  <span>
                    <strong>Harbor API</strong>
                    <small>TTFT 1.2s · TPS 74</small>
                  </span>
                  <span className="welcome-channel-metric">
                    <strong>0.94×</strong>
                    <small><span aria-hidden="true" className="welcome-status-dot" /> 99.6%</small>
                  </span>
                </li>
              </ol>
              <div className="welcome-endpoint">
                <Icon name="key" size={20} />
                <span>
                  <small>统一 Base URL</small>
                  <strong>api.aihub.example/v1</strong>
                </span>
                <Icon name="copy" size={16} />
              </div>
            </div>
          </div>
        </section>

        <section aria-label="平台能力摘要" className="welcome-proof-strip">
          {proofItems.map((item) => (
            <article className="welcome-proof-card" key={item.title}>
              <span className="welcome-outline-icon">
                <Icon name={item.icon} size={19} />
              </span>
              <span>
                <strong>{item.title}</strong>
                <small>{item.detail}</small>
              </span>
            </article>
          ))}
        </section>

        <section aria-labelledby="roles-title" className="welcome-section welcome-roles" id="capabilities">
          <div className="welcome-section-heading welcome-section-heading-split">
            <h2 id="roles-title">供需两边，都有清楚的价值</h2>
            <p>消费者获得稳定入口；共享者把已经充值的优质渠道变成可持续供给。</p>
          </div>
          <div className="welcome-role-grid">
            <article className="welcome-role-card welcome-role-consumer">
              <div className="welcome-role-topline">
                <span className="welcome-role-tag">API 消费者</span>
                <span className="welcome-role-icon"><Icon name="terminal" size={24} /></span>
              </div>
              <h3>少折腾网关，专心使用模型</h3>
              <p>用一个平台 Key 访问被允许的模型，自己决定每个模型的渠道回退顺序。</p>
              <ul>
                <li><Icon name="check" size={16} /> 一个 Key 管理多个模型</li>
                <li><Icon name="check" size={16} /> 按价格与质量挑选渠道</li>
                <li><Icon name="check" size={16} /> 失败自动切到下一优先级</li>
              </ul>
              <a className="welcome-role-action" href="/login">
                登录后开始 <Icon name="arrow" size={16} />
              </a>
            </article>

            <article className="welcome-role-card welcome-role-provider">
              <div className="welcome-role-topline">
                <span className="welcome-role-tag">渠道共享者</span>
                <span className="welcome-role-icon"><Icon name="share" size={24} /></span>
              </div>
              <h3>让好渠道被更多人使用</h3>
              <p>连接已有中转站账户，选择支持的模型与原生 API 格式，自主设置价格倍率。</p>
              <ul>
                <li><Icon name="check" size={16} /> 上游 Key 写入后不可回显</li>
                <li><Icon name="check" size={16} /> 每个模型单独配置倍率</li>
                <li><Icon name="check" size={16} /> 收入与调用记录独立可查</li>
              </ul>
              <a className="welcome-role-action" href="/login">
                登录后开始 <Icon name="arrow" size={16} />
              </a>
            </article>
          </div>
        </section>

        <section aria-labelledby="market-title" className="welcome-section welcome-market" id="market">
          <div className="welcome-section-heading welcome-section-heading-split">
            <div>
              <span className="welcome-eyebrow">API 市场</span>
              <h2 id="market-title">不是只比价格的渠道目录</h2>
            </div>
            <p>价格、性能和稳定性分别展示。先看事实，再决定把谁加入模型协议池。</p>
          </div>

          <div aria-label="API 市场界面示意" className="welcome-market-demo">
            <div className="welcome-market-toolbar">
              <span className="welcome-faux-search">⌕&nbsp;&nbsp; 搜索模型或共享者</span>
              <span className="welcome-sort"><Icon name="tune" size={16} /> 成功率优先</span>
            </div>
            <div className="welcome-market-body">
              <aside aria-hidden="true" className="welcome-market-filter">
                <strong>筛选</strong>
                <span className="welcome-filter-label">API 格式</span>
                <span>▣&nbsp; Responses</span>
                <span>□&nbsp; Chat Completions</span>
                <span>□&nbsp; Anthropic</span>
                <span>□&nbsp; Gemini</span>
                <div className="welcome-demo-divider" />
                <span className="welcome-filter-label">优先查看</span>
                <span>›&nbsp; 成功率最高</span>
                <span>›&nbsp; TTFT 最快</span>
                <span>›&nbsp; TPS 最高</span>
              </aside>
              <div className="welcome-market-table-wrap">
                <table className="welcome-market-table">
                  <caption className="welcome-sr-only">API 渠道价格与质量指标示意</caption>
                  <thead>
                    <tr>
                      <th>模型</th>
                      <th>共享者</th>
                      <th>价格倍率</th>
                      <th>TTFT</th>
                      <th>TPS</th>
                      <th>可用率</th>
                      <th>评分</th>
                    </tr>
                  </thead>
                  <tbody>
                    {marketRows.map((row) => (
                      <tr className={row.featured ? 'welcome-market-row-featured' : undefined} key={`${row.model}-${row.provider}`}>
                        <td><strong>{row.model}</strong><small>Responses · Tool use</small></td>
                        <td>{row.provider}</td>
                        <td><strong>{row.multiplier}</strong></td>
                        <td>{row.ttft}</td>
                        <td>{row.tps}</td>
                        <td><span aria-hidden="true" className="welcome-status-dot" /> {row.availability}</td>
                        <td>☆ <strong>{row.score}</strong></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="welcome-market-mobile-list">
                {marketRows.slice(0, 3).map((row) => (
                  <article className={row.featured ? 'welcome-market-mobile-featured' : undefined} key={`${row.model}-${row.provider}-mobile`}>
                    <div>
                      <strong>{row.model}</strong>
                      <small>{row.provider}</small>
                    </div>
                    <span className="welcome-rate-pill">{row.multiplier}</span>
                    <dl>
                      <div><dd>{row.ttft}</dd><dt>TTFT</dt></div>
                      <div><dd>{row.tps}</dd><dt>TPS</dt></div>
                      <div><dd>{row.availability}</dd><dt>可用率</dt></div>
                    </dl>
                  </article>
                ))}
              </div>
            </div>
          </div>
        </section>

        <section aria-labelledby="workflow-title" className="welcome-section welcome-workflow" id="workflow">
          <div className="welcome-section-heading welcome-section-heading-split">
            <div>
              <span className="welcome-eyebrow">从连接到清算</span>
              <h2 id="workflow-title">四步形成共享闭环</h2>
            </div>
            <p>保持原生协议，不把复杂算法塞进首版。每一步都能看见、能控制。</p>
          </div>
          <ol className="welcome-workflow-grid">
            {workflowSteps.map((step) => (
              <li key={step.number}>
                <div className="welcome-step-topline">
                  <span className="welcome-step-number">{step.number}</span>
                  <Icon name={step.icon} size={24} />
                </div>
                <h3>{step.title}</h3>
                <p>{step.body}</p>
                <span className="welcome-step-signal"><Icon name="arrow" size={15} /> {step.signal}</span>
              </li>
            ))}
          </ol>
        </section>

        <section aria-labelledby="security-title" className="welcome-transparency" id="security">
          <div className="welcome-transparency-copy">
            <span className="welcome-dark-eyebrow"><Icon name="eye-off" size={15} /> 透明，而不是黑箱</span>
            <h2 id="security-title">知道请求去了哪里，<br />也知道积分去了哪里</h2>
            <p>平台把路由、质量指标和账本留在明面上，同时缩小真正需要保存的数据范围。</p>
            <div className="welcome-trust-list">
              <article>
                <span className="welcome-light-icon"><Icon name="lock" size={20} /></span>
                <span><strong>上游 Key 加密保存</strong><small>写入后不可回显，不进入日志或设计资产</small></span>
              </article>
              <article>
                <span className="welcome-light-icon"><Icon name="eye-off" size={20} /></span>
                <span><strong>不保存请求与响应正文</strong><small>保留调用指标、错误码和错误消息</small></span>
              </article>
            </div>
          </div>

          <div aria-label="请求路由与清算记录示意" className="welcome-trace-card">
            <div className="welcome-trace-header">
              <span><small>REQUEST TRACE</small><strong>req_8F2A · 通用推理模型</strong></span>
              <span className="welcome-trace-state">●&nbsp; 已完成</span>
            </div>
            <div className="welcome-dark-divider" />
            <ol className="welcome-trace-list">
              {traceSteps.map((step) => (
                <li key={step.title}>
                  <span className="welcome-trace-icon"><Icon name={step.icon} size={18} /></span>
                  <span><strong>{step.title}</strong><small>{step.meta}</small></span>
                  <span className={step.success ? 'welcome-trace-state welcome-trace-success' : 'welcome-trace-state'}>{step.state}</span>
                </li>
              ))}
            </ol>
            <div className="welcome-retention-note">
              <Icon name="database" size={20} />
              <span><strong>请求正文不留存</strong><small>指标与错误用于质量判断</small></span>
            </div>
          </div>
        </section>

        <section aria-labelledby="c2c-title" className="welcome-section welcome-c2c" id="c2c">
          <div className="welcome-section-heading welcome-section-heading-split">
            <div>
              <span className="welcome-eyebrow">积分 C2C</span>
              <h2 id="c2c-title">赚到的积分，可以继续流转</h2>
            </div>
            <p>买单和卖单都能部分成交。平台托管积分，法币由买卖双方直接完成。</p>
          </div>
          <div className="welcome-c2c-layout">
            <div aria-label="积分 C2C 订单界面示意" className="welcome-orderbook">
              <div className="welcome-orderbook-title">
                <strong>○&nbsp;&nbsp;积分市场</strong>
                <span>示意订单</span>
              </div>
              <div className="welcome-guide"><span>建议参考单位</span><strong>1 积分 ≈ 1 元</strong></div>
              <div className="welcome-order-grid">
                <article>
                  <div className="welcome-order-topline"><span>买单</span><strong>¥1.00 / 积分</strong></div>
                  <h3>剩余 200 / 1,000</h3>
                  <p><span>微信</span><span>支付宝</span></p>
                  <small>支持部分成交</small>
                </article>
                <article>
                  <div className="welcome-order-topline"><span>卖单</span><strong>¥1.01 / 积分</strong></div>
                  <h3>剩余 900 / 1,000</h3>
                  <p><span>微信</span><span>支付宝</span></p>
                  <small>支持部分成交</small>
                </article>
              </div>
            </div>
            <ol className="welcome-c2c-steps">
              {c2cSteps.map((step) => (
                <li key={step.title}>
                  <span className="welcome-dark-icon"><Icon name={step.icon} size={21} /></span>
                  <span><strong>{step.title}</strong><small>{step.body}</small></span>
                </li>
              ))}
            </ol>
          </div>
        </section>

        <section aria-labelledby="welcome-cta-title" className="welcome-cta-section">
          <div className="welcome-cta-band">
            <div>
              <span className="welcome-eyebrow">INVITATION ONLY</span>
              <h2 id="welcome-cta-title">已有管理员发来的账号？</h2>
              <p>登录控制台，创建你的平台 Key，或把可信渠道分享给小圈子。</p>
            </div>
            <div className="welcome-cta-action">
              <LoginLink>登录控制台</LoginLink>
              <small>暂不开放自由注册</small>
            </div>
          </div>
        </section>
      </main>

      <footer className="welcome-footer">
        <div className="welcome-footer-main">
          <div>
            <Brand />
            <p>把人民群众发现的好渠道，变成共享的可靠入口。</p>
          </div>
          <nav aria-label="页脚导航">
            <a href="#capabilities">产品能力</a>
            <a href="#market">API 市场</a>
            <a href="#workflow">工作方式</a>
            <a href="#security">安全边界</a>
          </nav>
        </div>
        <div className="welcome-footer-bottom">
          <span>© Oh My AIHub</span>
          <span className="welcome-members-only"><Icon name="lock" size={15} /> 仅限受邀成员</span>
        </div>
      </footer>
    </div>
  )
}
