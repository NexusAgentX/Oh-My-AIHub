import { useState } from 'react'
import { Button, ButtonLink, Checkbox, InlineError, SelectField } from '../ui'
import { errorMessage } from '../api/query'
import { formatDateTime } from '../money/time'
import { useDetectionJobs } from '../detection/queries'
import { useContributionReportChoices } from './queries'

/** A task is offered only after its target has been verified in list metadata. */
export function ReportPicker({ targetID, selected, onToggle, from }: { targetID: string; selected: string[]; onToggle: (id: string, checked: boolean) => void; from: { to: string; label: string } }) {
  const [cursor, setCursor] = useState(''), [task, setTask] = useState({ id: '', label: '' }), [reportCursor, setReportCursor] = useState('')
  const jobs = useDetectionJobs(cursor)
  const options = jobs.data?.items.filter(job => job.target.binding.target_id === targetID) ?? []
  const reports = useContributionReportChoices(task.id, reportCursor)
  return <div className="stack-form">
    <SelectField label="检测任务" value={task.id} onChange={event => { const job = options.find(item => item.id === event.target.value); setTask(job ? { id: job.id, label: formatDateTime(job.created_at) } : { id: '', label: '' }); setReportCursor('') }}>
      <option value="">请选择此目标的检测任务</option>
      {task.id && !options.some(job => job.id === task.id) && <option value={task.id}>{task.label}</option>}
      {options.map(job => <option key={job.id} value={job.id}>{formatDateTime(job.created_at)} · {job.finished_scenarios}/{job.scenarios} 场景</option>)}
    </SelectField>
    <div className="form-actions"><Button type="button" variant="quiet" disabled={!cursor || jobs.isFetching} onClick={() => setCursor('')}>最新任务</Button><Button type="button" variant="quiet" disabled={!jobs.data?.next_cursor || jobs.isFetching} onClick={() => setCursor(jobs.data?.next_cursor ?? '')}>更早任务</Button></div>
    <InlineError>{jobs.isError && errorMessage(jobs.error, '无法加载检测任务')}</InlineError>
    {task.id && <><InlineError>{reports.isError && errorMessage(reports.error, '无法加载报告')}</InlineError>{reports.isPending && <p className="muted-copy" role="status">正在加载报告…</p>}{reports.data?.items.length === 0 && <p className="muted-copy">此任务尚未生成可关联的报告。</p>}
      {reports.data?.items.map(report => <div key={report.id} className="contribution-order"><Checkbox label={`${formatDateTime(report.created_at)} · ${report.complete ? '完整报告' : '未完成'}`} checked={selected.includes(report.id)} disabled={!report.complete} onChange={event => onToggle(report.id, event.target.checked)} /><ButtonLink size="sm" to={`/supply/detection/reports/${report.id}`} state={{ from }}>查看</ButtonLink></div>)}
      <div className="form-actions"><Button type="button" variant="quiet" disabled={!reportCursor || reports.isFetching} onClick={() => setReportCursor('')}>最新报告</Button><Button type="button" variant="quiet" disabled={!reports.data?.next_cursor || reports.isFetching} onClick={() => setReportCursor(reports.data?.next_cursor ?? '')}>更早报告</Button><Button type="button" variant="quiet" loading={reports.isFetching} onClick={() => void reports.refetch()}>刷新报告</Button></div>
    </>}
    {selected.filter(id => !reports.data?.items.some(report => report.id === id)).map((id, index) => <div className="contribution-order" key={id}><ButtonLink variant="quiet" to={`/supply/detection/reports/${id}`} state={{ from }}>已选报告 {index + 1}</ButtonLink><Button type="button" variant="quiet" onClick={() => onToggle(id, false)}>移除关联</Button></div>)}
  </div>
}
