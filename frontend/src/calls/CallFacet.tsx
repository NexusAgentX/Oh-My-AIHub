import { Button, Notice } from '../ui'
import { analysisDimensions } from './labels'
import type { CallListParams } from './queries'

export function CallFacet({ params, onClear }: { params: CallListParams; onClear: () => void }) {
  if (!params.dimension) return null
  const labels = [
    `${analysisDimensions[params.dimension] ?? '分析条件'}：${params.dimension_value || '未提供'}`,
    ...(params.second_dimension ? [`${analysisDimensions[params.second_dimension] ?? '交叉条件'}：${params.second_value || '未提供'}`] : []),
  ]
  return <Notice tone="info" action={<Button variant="quiet" size="sm" onClick={onClear}>清除下钻</Button>}>{labels.join(' · ')}</Notice>
}
