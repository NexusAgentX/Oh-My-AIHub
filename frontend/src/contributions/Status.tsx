import { useState } from 'react'
import {
  Card,
  EmptyState,
  PageHeader,
  Pagination,
  QueryBoundary,
} from '../ui'
import { useContributions, type Kind } from './queries'
import { ContributionDialog, StateBadge } from './Editor'
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

/** 只读状态页：成员查看自己每份提案的审核状态与驳回理由。 */
export function MyContributionsPage() {
  const [cursor, setCursor] = useState('')
  const query = useContributions(false, cursor)
  return (
    <>
      <PageHeader
        title="我的贡献"
        description="在供应商、分组、模型报价等页面就地提交修改；审核通过后才更新线上数据。"
      />
      <Card>
        <QueryBoundary
          query={query}
          isEmpty={(d) => d.items.length === 0}
          empty={
            <Card>
              <EmptyState
                title="还没有贡献"
                description="到供应商、分组或模型页发起修改，草稿会出现在这里。"
              />
            </Card>
          }
        >
          {(data) => (
            <div className="contribution-list">
              {data.items.map((v) => {
                const editable = ['draft', 'rejected', 'withdrawn'].includes(
                  v.state,
                )
                return (
                  <article key={v.id}>
                    <div>
                      <strong>{v.title}</strong>
                      <p className="muted-copy">
                        {kinds[v.kind as Kind] ?? v.kind} ·{' '}
                        {stamp(v.updated_at)}
                      </p>
                      {v.review_reason && (
                        <p className="muted-copy">
                          驳回理由：{v.review_reason}
                        </p>
                      )}
                    </div>
                    <StateBadge value={v.state} />
                    <ContributionDialog
                      draftID={v.id}
                      label={editable ? '继续编辑' : '查看'}
                      title={v.title}
                    />
                  </article>
                )
              })}
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
