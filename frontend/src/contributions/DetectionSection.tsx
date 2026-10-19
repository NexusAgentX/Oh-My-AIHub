import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { components } from '../api/schema.gen'
import { errorMessage } from '../api/query'
import { useDetectionJobs } from '../detection/queries'
import { formatDateTime } from '../money/time'
import { Badge, Button, ButtonLink, Disclosure, InlineError, Notice } from '../ui'
import { ReportPicker } from './ReportPicker'

const jobNames: Record<string, string> = { queued: '排队中', running: '检测中', finished: '已结束', cancelled: '已取消', interrupted: '已中断', failed: '检测器异常', suite_pending: '套件待实现', outdated: '配置已变化' }
const targetNames: Record<string, string> = { ready: '报告已就绪', report_required: '需要完整报告', suite_pending: '检测套件待实现', outdated: '配置已变化', invalid: '请补充内容' }
type Target = components['schemas']['ContributionTargetReview']

export function DetectionSection({ draftID, targets, taskIDs, selected, onChange, editable, dirty, blocked, from }: { draftID: string; targets: Target[]; taskIDs: string[]; selected: string[]; onChange: (ids: string[]) => void; editable: boolean; dirty: boolean; blocked: boolean; from: { to: string; label: string } }) {
  const jobs = useDetectionJobs('', taskIDs.length > 0, draftID), client = useQueryClient()
  const terminalVersion = jobs.data?.items.filter(job => !job.execution_active).map(job => `${job.id}:${job.updated_at}`).join('|')
  const previousTerminal = useRef<string | undefined>(undefined)
  useEffect(() => {
    if (terminalVersion === undefined) return
    if (previousTerminal.current !== undefined && terminalVersion !== previousTerminal.current) void client.invalidateQueries({ queryKey: ['contributions', 'detail', draftID] })
    previousTerminal.current = terminalVersion
  }, [terminalVersion, client, draftID])
  const [manualOwners, setManualOwners] = useState<Record<string, string>>({})
  const owners = { ...Object.fromEntries(targets.flatMap(target => target.report ? [[target.report.report_id, target.id]] : [])), ...manualOwners }
  const unassigned = selected.filter(id => !owners[id])
  return <>
    <Notice tone="info">全部适用项目测完后即可提交，检测失败或不支持也可以提交。未执行项目不算完成。</Notice>
    {!targets.length && <p className="muted-copy">资料未填完整时，请先保存。纯供应商资料及紧急停用、删除不要求付费检测。</p>}
    <InlineError>{jobs.isError && errorMessage(jobs.error, '检测进度加载失败，可进入任务查看')}</InlineError>
    <div className="contribution-targets">{targets.map(target => {
      const latest = jobs.data?.items.find(job => job.target.binding.target_id === target.id)
      const ids = selected.filter(id => owners[id] === target.id)
      const toggle = (id: string, checked: boolean) => {
        setManualOwners(old => ({ ...old, [id]: target.id }))
        onChange(checked ? [...selected.filter(item => owners[item] !== target.id && item !== id), id] : selected.filter(item => item !== id))
      }
      return <section key={target.id} className="contribution-section"><div className="contribution-target-heading"><strong>{target.model_id || '分组检测'}</strong><Badge tone={target.report ? 'success' : 'warning'}>{targetNames[target.state] ?? target.state}</Badge></div>
        {latest && <div className="form-actions"><Badge tone={latest.execution_active ? 'info' : ['failed', 'interrupted'].includes(latest.state) ? 'danger' : 'neutral'}>{jobNames[latest.state] ?? latest.state}{latest.execution_active && ` · ${latest.finished_scenarios}/${latest.scenarios}`}</Badge><ButtonLink size="sm" variant="quiet" to={`/supply/detection/${latest.id}`} state={{ from }}>查看任务</ButtonLink></div>}
        {target.report && <p><ButtonLink size="sm" variant="quiet" to={`/supply/detection/reports/${target.report.report_id}`} state={{ from }}>{target.report.reused ? '查看复用报告' : '查看报告'}</ButtonLink> <span className="muted-copy">{formatDateTime(target.report.finished_at)}</span></p>}
        {editable && <><ButtonLink variant="secondary" size="sm" to={`/supply/detection/new?draft_id=${draftID}&target_kind=${target.kind}&target_id=${target.id}`} state={{ from }} aria-disabled={blocked || dirty} onClick={event => { if (blocked || dirty) event.preventDefault() }}>{target.report || latest ? '重新全测' : '开始全测'}</ButtonLink><Disclosure title="选择此目标的报告" lazy><fieldset className="contribution-fields" disabled={blocked}><ReportPicker targetID={target.id} selected={ids} onToggle={toggle} from={from} /></fieldset></Disclosure></>}
      </section>
    })}</div>
    {unassigned.length > 0 && <section className="contribution-section"><h3>未匹配到当前目标的报告</h3>{unassigned.map((id, index) => <div key={id} className="contribution-order"><ButtonLink variant="quiet" to={`/supply/detection/reports/${id}`} state={{ from }}>报告 {index + 1}</ButtonLink>{editable && <Button variant="quiet" disabled={blocked} onClick={() => onChange(selected.filter(item => item !== id))}>移除关联</Button>}</div>)}</section>}
    <Disclosure title={`关联检测任务（${taskIDs.length}）`} lazy>{jobs.data?.next_cursor && <p className="muted-copy">当前显示最近 100 项的进度，历史任务可逐项打开。</p>}{taskIDs.map((id, index) => <p key={id}><ButtonLink variant="quiet" to={`/supply/detection/${id}`} state={{ from }}>检测任务 {index + 1}</ButtonLink></p>)}</Disclosure>
  </>
}
