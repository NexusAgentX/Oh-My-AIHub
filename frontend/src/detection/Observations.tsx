import { useState } from 'react'
import { Button, DataTable, Notice } from '../ui'
import type { Job } from './queries'

type Result = Job['results'][number]
type Capacity = NonNullable<Result['capacity']>[number]
const tokens = (value: number | null | undefined) => value == null ? '未知' : value.toLocaleString('zh-CN')
const stops: Record<Capacity['stop'], string> = {
  rounds: '已完成配置轮数', precision: '已达到探测目标精度', configured_bound: '已探到配置上界',
  non_monotonic: '边界不单调，保留已验证结果', unconfirmed: '无法继续确认边界', cancelled: '已取消',
}

function CapacityEvidence({ report }: { report: Capacity }) {
  const [expanded, setExpanded] = useState(false)
  const [page, setPage] = useState(0)
  const size = 50
  const steps = report.steps.slice(page * size, (page + 1) * size)
  return <section className="detection-observation">
    <h3>{report.kind === 'context' ? '上下文实测' : '最大输出实测'}</h3>
    <dl className="detection-versions">
      <dt>报价声明</dt><dd className="num">{tokens(report.declared)} token</dd>
      <dt>参数接受的探测点</dt><dd className="num">{tokens(report.parameter_accepted_lower)}</dd>
      <dt>长度限制探测点</dt><dd className="num">{tokens(report.parameter_rejected_upper)}</dd>
      <dt>实测成功下界</dt><dd className="num">{tokens(report.verified_lower)} token</dd>
      <dt>已证实失败上界</dt><dd className="num">{tokens(report.proven_failure_upper)} token</dd>
      <dt>结束状态</dt><dd>{stops[report.stop]}{!report.completed && ' · 未完成'}</dd>
    </dl>
    <p className="muted-copy">参数接受与实际达到分别记录；探到配置上界后，真实最大值仍可能更高。</p>
    {report.steps.some((step) => step.requested_approximate) && <p className="muted-copy">上下文请求目标为近似 token 数，实测下界取可靠用量；计数估算保留在逐轮证据中。</p>}
    <details onToggle={(event) => setExpanded(event.currentTarget.open)}>
      <summary>逐轮证据 · {report.steps.length} 次探测</summary>
      {expanded && <>
        <DataTable caption="长度探测证据" rows={steps} empty={<p className="muted-copy">尚无探测记录</p>} rowKey={(step) => `${step.round}-${step.recheck}-${step.requested}`} columns={[
          { key: 'round', header: '轮次', primary: true, cell: (step) => `${step.round}${step.recheck ? ` · 复核 ${step.recheck}` : ''}` },
          { key: 'requested', header: '请求目标', numeric: true, cell: (step) => `${step.requested_approximate ? '≈ ' : ''}${tokens(step.requested)}` },
          { key: 'accepted', header: '参数接受', cell: (step) => step.accepted ? '已接受' : step.length_error ? '长度限制' : '未确认' },
          { key: 'actual', header: '实际 token', numeric: true, cell: (step) => `${step.approximate && step.actual !== null ? '≈ ' : ''}${tokens(step.actual)}` },
          { key: 'semantic', header: '内容核验', cell: (step) => step.semantic ? '已核验' : '未确认' },
        ]} />
        {report.steps.length > size && <div className="form-actions"><Button variant="quiet" disabled={page === 0} onClick={() => setPage(page - 1)}>上一页</Button><span className="num">{page + 1}/{Math.ceil(report.steps.length / size)}</span><Button variant="quiet" disabled={(page + 1) * size >= report.steps.length} onClick={() => setPage(page + 1)}>下一页</Button></div>}
      </>}
    </details>
  </section>
}

function FingerprintEvidence({ report }: { report: NonNullable<Result['fingerprint']> }) {
  const [all, setAll] = useState(false)
  return <section className="detection-observation">
    <h3>模型指纹参考</h3>
    <Notice tone="info">结果表示参考库内的相似程度，无法据此确定模型身份或识别库外模型。</Notice>
    <p>有效样本 <span className="num">{report.used_outputs}/3</span> · {report.version}</p>
    {report.decision === 'unscorable' ? <p>有效样本不足，无法评分。</p> : <>
      <DataTable caption="模型指纹参考排名" rows={all ? report.results : report.results.slice(0, 5)} rowKey={(row) => row.model} columns={[
        { key: 'model', header: '参考模型 ID', primary: true, cell: (row) => <span className="detection-code">{row.model}</span> },
        { key: 'score', header: '排名分数', numeric: true, cell: (row) => row.score.toFixed(3) },
        { key: 'probability', header: '参考库内概率', numeric: true, cell: (row) => row.probability == null ? '未知' : row.probability.toLocaleString('zh-CN', { style: 'percent', maximumFractionDigits: 2 }) },
      ]} />
      {report.results.length > 5 && <Button variant="quiet" onClick={() => setAll(!all)}>{all ? '只看前五项' : `查看全部 ${report.results.length} 项`}</Button>}
    </>}
    <details><summary>固定参考库</summary><dl className="detection-versions">
      <dt>参考库时间</dt><dd>{report.reference_built_at}</dd>
      <dt>算法提交</dt><dd className="detection-code">{report.commit}</dd>
      <dt>检测器 SHA-256</dt><dd className="detection-code">{report.detector_sha256}</dd>
      <dt>参考库 SHA-256</dt><dd className="detection-code">{report.reference_sha256}</dd>
    </dl></details>
  </section>
}

function ImageEvidence({ reports }: { reports: NonNullable<Result['images']> }) {
  const [page, setPage] = useState(0)
  const size = 20
  return <section className="detection-observation">
    <h3>图片产物与计价条件</h3>
    <DataTable caption="图片检测证据" rows={reports.slice(page * size, (page + 1) * size)} rowKey={(row) => `${row.attempt_ids[0]}-${JSON.stringify(row.variant)}-${row.transport}`} empty={<p className="muted-copy">尚无图片产物证据</p>} columns={[
      { key: 'variant', header: '计价条件', primary: true, cell: (row) => [row.variant.unit === 'request' ? '按请求' : row.variant.unit === 'successful_image' ? '按成功图片' : '默认条件', row.variant.size, row.variant.quality, row.variant.background, row.variant.service_tier].filter(Boolean).join(' · ') },
      { key: 'transport', header: '请求方式', cell: (row) => ({ json: 'JSON · inline', json_url: 'JSON · URL', multipart: 'multipart', sse: 'SSE' })[row.transport] },
      { key: 'count', header: '已核验 / 请求', numeric: true, cell: (row) => `${row.verified} / ${row.requested}` },
      { key: 'products', header: '实际产物', cell: (row) => row.products.map((product) => `${product.width}×${product.height} ${product.format}${product.transparent ? ' · 含透明区域' : ''}`).join('，') || '未确认' },
      { key: 'options', header: '计价条件核验', cell: (row) => row.options_confirmed && row.verified > 0 ? '已确认' : '未确认' },
      { key: 'usage', header: 'Token 用量', cell: (row) => row.usage_reliable ? '可靠' : '未知 · 不据此按 token 定价' },
    ]} />
    {reports.length > size && <div className="form-actions"><Button variant="quiet" disabled={page === 0} onClick={() => setPage(page - 1)}>上一页</Button><span className="num">{page + 1}/{Math.ceil(reports.length / size)}</span><Button variant="quiet" disabled={(page + 1) * size >= reports.length} onClick={() => setPage(page + 1)}>下一页</Button></div>}
  </section>
}

function SettingEvidence({ reports }: { reports: NonNullable<Result['settings']> }) {
  const [page, setPage] = useState(0)
  const size = 20
  const states: Record<string, string> = { pass: '已核验', partial: '部分支持', fail: '未通过', unsupported: '不支持', inconclusive: '未确认' }
  return <section className="detection-observation">
    <h3>缓存与实际服务档位</h3>
    <DataTable caption="缓存与服务档位实测" rows={reports.slice(page * size, (page + 1) * size)} rowKey={(row) => `${row.kind}-${row.configured}-${row.attempt_ids.join('-')}`} empty={<p className="muted-copy">尚无实测记录</p>} columns={[
      { key: 'configured', header: '检测条件', primary: true, cell: (row) => row.kind === 'cache' ? `缓存 · ${row.configured}` : row.configured },
      { key: 'wire', header: '请求值', cell: (row) => row.wire },
      { key: 'accepted', header: '参数接受', cell: (row) => row.accepted ? '已接受' : '未确认' },
      { key: 'actual', header: '实际观测', cell: (row) => row.kind === 'cache' ? row.usage_reliable ? `写入 ${tokens(row.cache_write_tokens)} · 读取 ${tokens(row.cache_read_tokens)} token` : '用量未知' : row.actual || '返档未知' },
      { key: 'state', header: '结论', cell: (row) => states[row.state] },
    ]} />
    {reports.some((row) => row.kind === 'cache') && <p className="muted-copy">缓存选项只核验参数与即时读写，不表示已验证保持或到期时长。</p>}
    {reports.length > size && <div className="form-actions"><Button variant="quiet" disabled={page === 0} onClick={() => setPage(page - 1)}>上一页</Button><span className="num">{page + 1}/{Math.ceil(reports.length / size)}</span><Button variant="quiet" disabled={(page + 1) * size >= reports.length} onClick={() => setPage(page + 1)}>下一页</Button></div>}
  </section>
}

export function DetectionObservations({ result }: { result: Pick<Result,'capacity'|'fingerprint'|'images'|'settings'|'quotas'> }) {
  return <>{result.capacity?.map((report) => <CapacityEvidence key={report.kind} report={report} />)}{result.fingerprint && <FingerprintEvidence report={result.fingerprint} />}{result.images && result.images.length > 0 && <ImageEvidence reports={result.images} />}{result.settings && result.settings.length > 0 && <SettingEvidence reports={result.settings} />}{result.quotas && result.quotas.length > 0 && <section className="detection-observation"><h3>上游剩余配额</h3><p className="muted-copy">本次凭据的响应观测值，不能据此换算金额余额。</p><dl className="detection-versions">{result.quotas.map(row=><div key={row.header}><dt className="detection-code">{row.header}</dt><dd className="num">{tokens(row.remaining)}</dd></div>)}</dl></section>}</>
}
