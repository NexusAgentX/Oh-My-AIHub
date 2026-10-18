import { useState } from 'react'
import { Button, ButtonLink, Checkbox, InlineError, SelectField } from '../ui'
import { errorMessage } from '../api/query'
import { useDetectionJobs } from '../detection/queries'
import { useContributionReportChoices } from './queries'
export function ReportPicker({
  tasks,
  targets,
  value,
  onChange,
}: {
  tasks: string[]
  targets: string[]
  value: string[]
  onChange: (value: string[]) => void
}) {
  const [cursor, setCursor] = useState(''),
    [task, setTask] = useState(tasks.at(-1) ?? ''),
    [reportCursor, setReportCursor] = useState('')
  const jobs = useDetectionJobs(cursor, targets.length > 0)
  const reports = useContributionReportChoices(task, reportCursor)
  const options =
    jobs.data?.items.filter((j) =>
      targets.includes(j.target.binding.target_id),
    ) ?? []
  return (
    <section className="contribution-section">
      <h3>关联检测报告</h3>
      <p className="muted-copy">
        选择检测任务及其完整报告。报告版本在提交后固定。
      </p>
      <SelectField
        label="检测任务"
        value={task}
        onChange={(e) => {
          setTask(e.target.value)
          setReportCursor('')
        }}
      >
        <option value="">请选择检测任务</option>
        {tasks
          .filter((id) => !options.some((j) => j.id === id))
          .map((id, index) => (
            <option key={id} value={id}>
              关联任务 {index + 1}
            </option>
          ))}
        {options.map((j) => (
          <option value={j.id} key={j.id}>
            {j.target.model || '分组检测'} ·{' '}
            {new Date(j.created_at).toLocaleString('zh-CN', { hour12: false })}
          </option>
        ))}
      </SelectField>
      {targets.length > 0 && (
        <div className="form-actions">
          <Button
            type="button"
            variant="quiet"
            disabled={!cursor}
            onClick={() => setCursor('')}
          >
            最新任务
          </Button>
          <Button
            type="button"
            variant="quiet"
            disabled={!jobs.data?.next_cursor}
            onClick={() => setCursor(jobs.data?.next_cursor ?? '')}
          >
            更早任务
          </Button>
        </div>
      )}
      <InlineError>
        {jobs.isError && errorMessage(jobs.error, '无法加载检测任务')}
      </InlineError>
      {task && (
        <>
          <InlineError>
            {reports.isError && errorMessage(reports.error, '无法加载报告')}
          </InlineError>
          {reports.data?.items.map((r) => (
            <div key={r.id} className="contribution-order">
              <Checkbox
                label={`${new Date(r.created_at).toLocaleString('zh-CN', { hour12: false })} · ${r.complete ? '完整报告' : '未完成'}`}
                checked={value.includes(r.id)}
                disabled={!r.complete}
                onChange={(e) =>
                  onChange(
                    e.target.checked
                      ? [...value, r.id]
                      : value.filter((id) => id !== r.id),
                  )
                }
              />
              <ButtonLink size="sm" to={`/detection/reports/${r.id}`}>
                查看
              </ButtonLink>
            </div>
          ))}
          <div className="form-actions">
            <Button
              type="button"
              variant="quiet"
              disabled={!reportCursor}
              onClick={() => setReportCursor('')}
            >
              最新报告
            </Button>
            <Button
              type="button"
              variant="quiet"
              disabled={!reports.data?.next_cursor}
              onClick={() => setReportCursor(reports.data?.next_cursor ?? '')}
            >
              更早报告
            </Button>
            <Button
              type="button"
              variant="quiet"
              onClick={() => void reports.refetch()}
            >
              刷新报告
            </Button>
          </div>
        </>
      )}
      {value.map((id, index) => (
        <div className="contribution-order" key={id}>
          <ButtonLink variant="quiet" to={`/detection/reports/${id}`}>
            已关联报告 {index + 1}
          </ButtonLink>
          <Button
            type="button"
            variant="quiet"
            onClick={() => onChange(value.filter((v) => v !== id))}
          >
            移除关联
          </Button>
        </div>
      ))}
    </section>
  )
}
