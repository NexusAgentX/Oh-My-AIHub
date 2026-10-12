import { PageHeader, QueryBoundary } from '../ui'
import { ModelCatalog } from './ModelCatalog'
import { useModels } from './queries'

export function ModelsPage() {
  const models = useModels()
  return <>
    <PageHeader title="模型" />
    <QueryBoundary errorFallback="模型列表加载失败" query={models}>
      {(data) => <ModelCatalog models={data.items} />}
    </QueryBoundary>
  </>
}
