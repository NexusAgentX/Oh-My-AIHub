import { useState } from 'react'
import { useParams } from 'react-router-dom'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'
import {
  Button,
  ButtonLink,
  Card,
  EmptyState,
  InlineError,
  Notice,
  PageHeader,
  Pagination,
  QueryBoundary,
  SelectField,
  TextField,
} from '../ui'
import {
  useContributions,
  useNewContribution,
  type Kind,
} from './queries'
import { ContributionEditor, ContributionDialog, StateBadge } from './Editor'
import './contributions.css'

const kinds: Record<Kind, string> = {
  supplier: '供应商',
  group: '分组',
  group_model: '模型报价',
  model_id: '模型 ID 治理',
}
function stamp(v: string) {
  return new Date(v).toLocaleString('zh-CN', { hour12: false })
}

/** 管理员审核队列：成员提交后在此通过或驳回。 */
export function ContributionQueuePage() {
  const [cursor, setCursor] = useState('')
  const query = useContributions(true, cursor)
  return (
    <>
      <PageHeader
        title="贡献审核"
        description="通过与驳回都会通知成员；批准后线上市场在同一事务内更新。"
      />
      <Card>
        <QueryBoundary
          query={query}
          isEmpty={(d) => d.items.length === 0}
          empty={<EmptyState title="暂无待审核贡献" description="成员提交后会出现在这里。" />}
        >
          {(data) => (
            <div className="contribution-list">
              {data.items.map((v) => (
                <article key={v.id}>
                  <div>
                    <strong>{v.title}</strong>
                    <p className="muted-copy">
                      {kinds[v.kind as Kind] ?? v.kind} ·{' '}
                      {stamp(v.updated_at)}
                    </p>
                  </div>
                  <StateBadge value={v.state} />
                  <ButtonLink size="sm" to={`/admin/contributions/${v.id}`}>
                    审核
                  </ButtonLink>
                </article>
              ))}
            </div>
          )}
        </QueryBoundary>
      </Card>
      {query.data && query.data.items.length > 0 && (
        <Pagination
          firstLabel="回到最新"
          hasPrevious={Boolean(cursor)}
          onPrevious={() => setCursor('')}
          hasNext={Boolean(query.data.next_cursor)}
          onNext={() => setCursor(query.data.next_cursor ?? '')}
        />
      )}
    </>
  )
}

export function ContributionReviewPage() {
  const { draftID = '' } = useParams()
  return (
    <>
      <PageHeader
        title="审核贡献"
        back={{ to: '/admin/contributions', label: '贡献审核' }}
      />
      <Card>
        <ContributionEditor draftID={draftID} review />
      </Card>
    </>
  )
}

/** 模型 ID 治理仍由管理员发起，走同一条审核链路。 */
export function ModelGovernancePage() {
  const { account } = useAuth()
  const [modelID, setModelID] = useState(''),
    [action, setAction] = useState<'disable' | 'restore'>('disable'),
    [draftID, setDraftID] = useState('')
  const create = useNewContribution()
  if (!account?.is_admin) {
    return <Notice tone="warning">只有管理员可以发起模型 ID 治理。</Notice>
  }
  return (
    <>
      <PageHeader
        title="模型 ID 治理"
        description="停用或恢复某个模型 ID 的全部报价，提交后在“贡献审核”中批准。"
      />
      <Card>
        <form
          className="stack-form"
          onSubmit={(e) => {
            e.preventDefault()
            create.mutate(
              { kind: 'model_id', target_id: modelID, action },
              { onSuccess: ({ draft }) => setDraftID(draft.id) },
            )
          }}
        >
          <TextField
            label="模型 ID"
            required
            value={modelID}
            onChange={(e) => setModelID(e.target.value)}
          />
          <SelectField
            label="操作"
            value={action}
            onChange={(e) =>
              setAction(e.target.value as 'disable' | 'restore')
            }
          >
            <option value="disable">停用</option>
            <option value="restore">恢复</option>
          </SelectField>
          <InlineError>
            {create.isError && errorMessage(create.error, '无法建立治理草稿')}
          </InlineError>
          <Button type="submit" loading={create.isPending}>
            建立治理草稿
          </Button>
        </form>
        {draftID && (
          <div className="contribution-section">
            <ContributionDialog
              draftID={draftID}
              label="打开治理草稿"
              title="模型 ID 治理"
            />
          </div>
        )}
      </Card>
    </>
  )
}
