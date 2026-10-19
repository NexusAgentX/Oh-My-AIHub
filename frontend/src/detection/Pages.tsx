import { formatDateTime } from '../money/time'
import { useBack } from '../ui/urlState'
import { useUnsavedGuard } from '../ui/UnsavedGuard'
import { DomainNav } from '../layouts/DomainNav'
import { useState, type FormEvent } from 'react'
import { Navigate, useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { errorMessage } from '../api/query'
import { Badge, Button, ButtonLink, Card, EmptyState, InlineError, Metric, MetricGrid, Notice, PageHeader, PasswordField, QueryBoundary } from '../ui'
import { useDetectionAction, useDetectionArtifacts, useDetectionJob, useDetectionJobs, useSavedDetectionReport,useDetectionReports, type Job } from './queries'
import './detection.css'
import { DetectionObservations } from './Observations'

const states: Record<string, string> = { queued: '排队中', running: '检测中', finished: '已结束', cancelled: '已取消', interrupted: '已中断', suite_pending: '检测套件待实现', failed: '检测器异常', outdated: '配置已变化', pass: '通过', partial: '部分支持', fail: '未通过', unsupported: '不支持', inconclusive: '无法确认', unobserved: '未观测', untested: '未执行' }
const reasons: Record<string, string> = { ok: '已核验', unsupported: '上游不支持', assertion_failed: '断言未通过', prerequisite_failed: '前置未通过，无法继续核验', external_error: '上游或网络异常', detector_error: '检测器异常', not_observed: '条件未出现', client_disabled: '客户端未启用', platform_unavailable: '平台代理暂不可用', body_limit: '超出单次大小保护上限', timeout: '单次请求超时', network_error: '网络错误', invalid_response: '响应不符合约定', cancelled: '已取消', interrupted: '进程中断' }
const layers: Record<string, string> = { upstream_protocol: '上游协议', gateway_proxy: '平台代理', official_cli: '官方 CLI' }
function State({ state }: { state: string }) { return <Badge tone={state === 'pass' ? 'success' : ['running', 'queued'].includes(state) ? 'info' : ['failed', 'fail'].includes(state) ? 'danger' : 'neutral'}>{states[state] ?? state}</Badge> }
function stamp(value: string) { return formatDateTime(value) }

export function DetectionPage() {
  const [cursor, setCursor] = useState(''), [params] = useSearchParams(), location = useLocation()
  const query = useDetectionJobs(cursor), { account } = useAuth()
  if (params.has('target_id')) return <Navigate to={`/supply/detection/new${location.search}`} state={location.state} replace />
  return <><PageHeader title="检测任务" actions={account?.is_admin && <ButtonLink to="/admin/settings/detection">搜索参数</ButtonLink>} /><DomainNav domain="供应" />
    <Card title="任务记录"><QueryBoundary query={query} isEmpty={data => data.items.length === 0} empty={<EmptyState title="还没有检测记录" description="从贡献、供应商分组或模型页面发起检测。" action={<ButtonLink to="/suppliers">浏览供应商</ButtonLink>} />}>
      {data => <><div className="detection-list" aria-busy={query.isFetching}>{data.items.map(job => <article className="detection-row" key={job.id}><div><strong>{job.target.model || '分组检测'}</strong><p className="muted-copy">{stamp(job.created_at)} · {job.finished_scenarios}/{job.scenarios} 场景</p></div><State state={job.state} /><ButtonLink size="sm" to={`/supply/detection/${job.id}`} state={{ from: { to: location.pathname + location.search, label: '检测任务' } }}>查看任务</ButtonLink></article>)}</div><div className="form-actions"><Button variant="quiet" disabled={!cursor || query.isFetching} onClick={() => setCursor('')}>回到最新</Button><Button variant="secondary" disabled={!data.next_cursor || query.isFetching} onClick={() => setCursor(data.next_cursor ?? '')}>更早任务</Button></div></>}
    </QueryBoundary></Card></>
}

export function NewDetectionPage() {
  const [params] = useSearchParams(), location = useLocation(), navigate = useNavigate()
  const targetID = params.get('target_id') ?? '', kind = params.get('target_kind'), draftID = params.get('draft_id') || undefined
  const canStart = targetID !== '' && (kind === 'group' || kind === 'group_model')
  const [key, setKey] = useState(''), action = useDetectionAction()
  const back = useBack(draftID ? `/supply/contributions/${draftID}` : '/supply/detection', draftID ? '贡献详情' : '检测任务')
  const allowNavigation = useUnsavedGuard(Boolean(key), { busy: action.isPending })
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!canStart || !key || action.isPending) return
    const secret = key; setKey('')
    action.mutate({ action: 'start', key: secret, target: { target_id: targetID, target_kind: kind, draft_id: draftID } }, { onSuccess: ({ job }) => { allowNavigation(); navigate(`/supply/detection/${job.id}`, { replace: true, state: { from: location.state?.from ?? back } }) } })
  }
  return <><PageHeader title="发起检测" back={back} />{canStart ? <Card title="全量检测"><form className="stack-form" onSubmit={submit}>
    <Notice tone="warning">检测会调用上游并产生费用。请先在上游设置消费限额。</Notice>
    <PasswordField autoComplete="off" label="本次检测使用的上游 API Key" value={key} onChange={event => setKey(event.target.value)} required disabled={action.isPending} />
    <InlineError>{action.isError && errorMessage(action.error, '无法创建检测，请重新填写凭据后重试')}</InlineError><Button type="submit" disabled={!key} loading={action.isPending}>开始全量检测</Button>
  </form></Card> : <EmptyState title="请先选择检测目标" action={<ButtonLink to="/suppliers">浏览供应商</ButtonLink>} />}</>
}

function Report({ job,readOnly=false }: { job: Job;readOnly?:boolean }) {
  const [key, setKey] = useState('')
  const [selected, setSelected] = useState<string[]>([])
 const navigate=useNavigate(), location=useLocation()
 const action = useDetectionAction()
 const allowNavigation=useUnsavedGuard(Boolean(key), { busy: action.isPending })
  const artifacts = useDetectionArtifacts(job.id,job.updated_at,job.execution_active,!readOnly)
  const active = !readOnly && job.execution_active
  const resumable = !readOnly && !active && ['interrupted', 'cancelled', 'failed'].includes(job.state)
  const rerunnable = !readOnly && !active && (resumable || job.state === 'finished')
  const run = (mode: 'resume' | 'retest') => { const secret = key; setKey(''); action.mutate({ action: mode, id: job.id, key: secret, scenarios: selected },{onSuccess:({job:next})=>{if(next.id!==job.id){allowNavigation();navigate(`/supply/detection/${next.id}`,{state:location.state})}}}) }
  const results = new Map(job.results.map((r) => [r.scenario_id, r]))
  const estimated = job.plan.items.every((item) => item.estimated_http !== null) ? job.plan.items.reduce((sum, item) => sum + (item.estimated_http ?? 0), 0) : null
  return <>
    {job.state === 'suite_pending' && <Notice>检测套件尚未就绪，当前记录不能用于创建审核。</Notice>}
    {job.state === 'outdated' && <Notice>配置已变化，请从分组或模型页面新建检测。</Notice>}
    {job.state === 'finished' && <Notice tone={job.complete ? 'success' : 'warning'}>{job.complete ? '完整检测已结束，负向结论仍可提交审核。' : '此记录未满足创建全测条件，请查看未执行项与证据来源。'}</Notice>}
    {readOnly && <Notice tone="info">历史报告只读，后续复测保留独立的新报告。</Notice>}
 {job.continued_from_id && <Notice tone="info" action={<ButtonLink to={`/supply/detection/${job.continued_from_id}`}>查看上一任务</ButtonLink>}>已记录新的凭据来源，保留可复用的历史证据。</Notice>}
 <div className="detection-metrics"><MetricGrid><Metric label="场景覆盖" value={`${job.results.length}/${job.plan.items.length}`} progress={job.results.length / Math.max(1, job.plan.items.length) * 100} /><Metric label="HTTP 尝试" value={job.usage.http} hint={`预计 ${estimated ?? '未知'}`} /><Metric label="已观测 token" value={job.usage.input_tokens + job.usage.output_tokens || '未知'} hint="费用估算：未知" /></MetricGrid></div>
    <Card title="执行与复测"><div className="stack-form">
      {job.state === 'cancelled' && active && <Notice tone="info">正在结束请求并清理检测资源…</Notice>}
 <p className="muted-copy">生成 {job.usage.generate} · 计数 {job.usage.count} · 查询 {job.usage.query} · 清理 {job.usage.cleanup} · 图片 {job.usage.images} · 工具 {job.usage.tools}</p>
      {active && job.state !== 'cancelled' && <Button variant="danger" loading={action.isPending} onClick={() => action.mutate({ action: 'cancel', id: job.id })}>取消检测</Button>}
      {rerunnable && <><PasswordField autoComplete="off" label="恢复／复测使用的上游 API Key" value={key} onChange={(e) => setKey(e.target.value)} /><div className="form-actions">{resumable && <Button disabled={!key} loading={action.isPending} onClick={() => run('resume')}>恢复未完成项目</Button>}<Button variant={resumable ? 'secondary' : 'primary'} disabled={!key || selected.length === 0} loading={action.isPending} onClick={() => run('retest')}>复测所选 {selected.length} 项</Button></div></>}
      <InlineError>{action.isError && errorMessage(action.error, '操作失败，请稍后重试')}</InlineError>
    </div></Card>
    <Card title="场景与独立断言"><div className="detection-list">{job.plan.items.map((item) => { const result = results.get(item.id); return <details className="detection-scenario" key={item.id}><summary><span className="detection-scenario-title">{item.suite_id.startsWith('codex-special.') && 'Codex 专项兼容 · '}<span className="detection-code">{item.id}</span></span><State state={result?.state ?? (item.implemented ? 'untested' : 'suite_pending')} /></summary>
      <div className="stack-form">{rerunnable && <label className="check-label"><input type="checkbox" checked={selected.includes(item.id)} onChange={(e) => setSelected((old) => e.target.checked ? [...old, item.id] : old.filter((id) => id !== item.id))} />选择此场景复测</label>}
        <p>{result?.source_task_id && result.source_task_id!==job.id && '复用历史证据 · '}{layers[item.layer]}{result && ` · ${result.source === 'synthetic' ? '合成机制证据' : '真实上游执行'} · ${reasons[result.reason] ?? result.reason}`}</p>
        {item.assertions.map((id) => { const assertion = result?.assertions.find((a) => a.id === id); return <div className="detection-assertion" key={id}><span className="detection-code">{item.assertion_labels[id] || id}</span><State state={assertion?.state ?? 'untested'} />{assertion && <span>{reasons[assertion.reason] ?? assertion.reason}</span>}</div> })}
        {result && <DetectionObservations result={result} />}
        {result?.attempts.map((attempt) => <p className="muted-copy detection-code" key={attempt.id}>{attempt.method} {attempt.path} · HTTP {attempt.status || '未收到响应'} · {attempt.duration_ms} ms · {reasons[attempt.reason] ?? attempt.reason}{attempt.entry_url && ` · ${attempt.entry_url}`}</p>)}
      </div></details> })}</div></Card>
    <Card title="待清理资源">{readOnly ? <p className="muted-copy">此轮结束时待清理 {job.pending_cleanup.length} 项</p> : <QueryBoundary query={artifacts}>{(data) => data.items.length === 0 ? <p className="muted-copy">无待清理资源</p> : <div className="detection-list">{data.items.map((item) => <p key={item.id} className="detection-code">{item.kind} · {item.native_id} · {reasons[item.reason] ?? item.reason}</p>)}</div>}</QueryBoundary>}</Card>
 {!readOnly && <ReportHistory taskID={job.id} version={job.updated_at} />}
    <Card title="版本与参数"><details><summary>检测时固定的配置</summary><dl className="detection-versions"><dt>范围</dt><dd>{job.plan.manifest_version}</dd><dt>代码</dt><dd>{job.plan.registry_version}</dd><dt>目标修订</dt><dd>{job.target.binding.target_revision}</dd><dt>上下文搜索</dt><dd>{job.parameters.context.method} · {job.parameters.context.low}–{job.parameters.context.high} · {job.parameters.context.precision_mode} {job.parameters.context.precision} · {job.parameters.context.rounds} 轮</dd><dt>最大输出搜索</dt><dd>{job.parameters.max_output.method} · {job.parameters.max_output.low}–{job.parameters.max_output.high} · {job.parameters.max_output.precision_mode} {job.parameters.max_output.precision} · {job.parameters.max_output.rounds} 轮</dd></dl></details></Card>
  </>
}
export function DetectionDetailPage() {
  const { taskID = '' } = useParams(), query = useDetectionJob(taskID), draftID = query.data?.job.target.draft_id
  const back = useBack(draftID ? `/supply/contributions/${draftID}` : '/supply/detection', draftID ? '贡献详情' : '检测任务')
  return <><PageHeader title={query.data?.job.target.model || '检测任务'} back={back} actions={query.data && <State state={query.data.job.state} />} /><QueryBoundary query={query}>{({ job }) => <Report key={job.id} job={job} />}</QueryBoundary></>
}

function ReportHistory({taskID,version}:{taskID:string;version:string}) { const [cursor,setCursor]=useState('');const query=useDetectionReports(taskID,cursor,version);return <Card title="历史报告"><QueryBoundary query={query}>{(data)=><><div className="detection-list">{data.items.length===0&&<p className="muted-copy">尚无已固化的报告</p>}{data.items.map((item)=><div key={item.id} className="detection-row"><span>{stamp(item.created_at)}</span><State state={item.state}/><ButtonLink size="sm" to={`/supply/detection/reports/${item.id}`}>查看此轮</ButtonLink></div>)}</div><div className="form-actions"><Button variant="quiet" disabled={!cursor} onClick={()=>setCursor('')}>回到最新</Button><Button variant="secondary" disabled={!data.next_cursor} onClick={()=>setCursor(data.next_cursor??'')}>下一页</Button></div></>}</QueryBoundary></Card>}
export function SavedDetectionReportPage() {
  const { reportID = '' } = useParams(), query = useSavedDetectionReport(reportID)
  const back = useBack(query.data ? `/supply/detection/${query.data.job.id}` : '/supply/detection', query.data ? '检测任务' : '检测任务列表')
  return <><PageHeader title={query.data?.job.target.model ? `${query.data.job.target.model} · 历史报告` : '历史检测报告'} back={back} /><QueryBoundary query={query}>{({ job }) => <Report key={reportID} job={job} readOnly />}</QueryBoundary></>
}
