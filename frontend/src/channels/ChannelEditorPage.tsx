import { useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { errorMessage } from '../api/query'
import type { Channel, ChannelDetail, ChannelCall } from '../api/types'
import { CallExplorer, FormatTags, channelCallColumns, outcomeLabels, outcomeOptions } from '../calls'
import { rangeFrom, type RangePreset } from '../money/time'
import {
  Button,
  Card,
  ConfirmDialog,
  Disclosure,
  Icon,
  InlineError,
  PageHeader,
  PasswordField,
  QueryBoundary,
  Segmented,
  SuccessMessage,
  Switch,
  TextField,
} from '../ui'
import { AdvancedFields } from './AdvancedFields'
import {
  advancedChangedCount,
  advancedFromChannel,
  advancedInput,
  applyTestResults,
  discoverSummary,
  emptyAdvanced,
  modelsInput,
  rowKey,
  rowsFromChannel,
  rowsFromDiscover,
  validateAdvanced,
  validateRows,
  type AdvancedForm,
  type ModelRow,
} from './channelForm'
import { ChannelEvents, ChannelStatsPanel } from './ChannelStatsPanel'
import { ModelRows } from './ModelRows'
import {
  useChannel,
  useCreateChannel,
  useDeleteChannel,
  useDiscoverChannel,
  useTestChannel,
  useUpdateChannel,
} from './queries'

const steps = ['连接', '选模型', '上架'] as const

function Stepper({ current }: { current: number }) {
  return (
    <ol className="stepper" aria-label="步骤">
      {steps.map((label, index) => (
        <li
          aria-current={index === current ? 'step' : undefined}
          className={index === current ? 'stepper-current' : index < current ? 'stepper-done' : ''}
          key={label}
        >
          <span className="stepper-index">{index < current ? <Icon name="check" size={14} /> : index + 1}</span>
          {label}
        </li>
      ))}
    </ol>
  )
}

function emptyRow(): ModelRow {
  return { key: rowKey(), sell: true, modelId: '', upstream: '', formats: ['openai_chat'], multiplier: '1', passed: {} }
}

/** 选模型区：读取结果提示、测试格式、手动添加与表格。 */
function ModelsSection({
  rows,
  onRows,
  summary,
  onTest,
  testing,
  testingModel,
  testError,
}: {
  rows: ModelRow[]
  onRows: (rows: ModelRow[]) => void
  summary: { total: number; matched: number } | null
  onTest: (modelId?: string) => void
  testing: boolean
  testingModel: string | null
  testError: string
}) {
  return (
    <div className="channel-models">
      {summary && (
        <p className="muted-copy">
          从上游读取到 {summary.total} 个模型，其中 {summary.matched} 个在平台目录里；格式已按模型预选，绿色是测试通过的
        </p>
      )}
      <div className="channel-models-actions">
        <Button disabled={testing || !rows.some((row) => row.sell && row.modelId)} loading={testing && testingModel === null} onClick={() => onTest()} size="sm" type="button" variant="secondary">
          测试格式
        </Button>
        <Button icon={<Icon name="plus" />} onClick={() => onRows([...rows, emptyRow()])} size="sm" type="button" variant="quiet">
          手动添加
        </Button>
      </div>
      <InlineError>{testError}</InlineError>
      <ModelRows onChange={onRows} onTest={onTest} rows={rows} testing={testing} testingModel={testingModel} />
    </div>
  )
}

/** 新建：三步向导（连接 → 选模型 → 上架）。「测试格式」会先以未上架状态保存渠道。 */
function ChannelWizard() {
  const navigate = useNavigate()
  const [step, setStep] = useState(0)
  const [name, setName] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [rows, setRows] = useState<ModelRow[]>([])
  const [summary, setSummary] = useState<{ total: number; matched: number } | null>(null)
  const [advanced, setAdvanced] = useState<AdvancedForm>(emptyAdvanced)
  const [channelId, setChannelId] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [testError, setTestError] = useState('')
  const [testing, setTesting] = useState(false)
  const [testingModel, setTestingModel] = useState<string | null>(null)
  const discover = useDiscoverChannel()
  const create = useCreateChannel()
  const update = useUpdateChannel()
  const test = useTestChannel()

  const connect = (event: FormEvent) => {
    event.preventDefault()
    setError('')
    if (!name.trim() || !baseUrl.trim() || !apiKey.trim()) {
      setError('请填写名称、Base URL 与上游 Key')
      return
    }
    discover.mutate(
      { base_url: baseUrl.trim(), api_key: apiKey.trim() },
      {
        onSuccess: (result) => {
          setBaseUrl(result.base_url)
          setRows(rowsFromDiscover(result.upstream_models))
          setSummary(discoverSummary(result.upstream_models))
          setStep(1)
        },
        onError: (caught) => setError(errorMessage(caught, '读取上游模型失败')),
      },
    )
  }

  const body = (status: 'listed' | 'unlisted') => ({
    name: name.trim(),
    base_url: baseUrl.trim(),
    api_key: apiKey.trim(),
    status,
    models: modelsInput(rows),
    advanced: advancedInput(advanced),
  })

  /** 保存（未上架时创建，之后更新）并返回渠道。 */
  const save = async (status: 'listed' | 'unlisted'): Promise<Channel> => {
    if (channelId) return update.mutateAsync({ id: channelId, body: body(status) })
    const channel = await create.mutateAsync(body(status))
    setChannelId(channel.id)
    return channel
  }

  const runTest = async (modelId?: string) => {
    if (testing) return
    setTestError('')
    const problem = validateRows(rows)
    if (problem) {
      setTestError(problem)
      return
    }
    setTesting(true)
    setTestingModel(modelId ?? null)
    try {
      const channel = await save('unlisted')
      const result = await test.mutateAsync({
        id: channel.id,
        body: { model_ids: modelId ? [modelId] : rows.filter((row) => row.sell && row.modelId).map((row) => row.modelId), apply: false },
      })
      setRows((current) => applyTestResults(current, result.results))
    } catch (caught) {
      setTestError(errorMessage(caught, '测试失败，请重试'))
    } finally {
      setTesting(false)
    }
  }

  const next = () => {
    const problem = validateRows(rows) || validateAdvanced(advanced)
    setError(problem)
    if (!problem) setStep(2)
  }

  const publish = async () => {
    setError('')
    try {
      const channel = await save('listed')
      navigate(`/channels/${channel.id}`, { replace: true })
    } catch (caught) {
      setError(errorMessage(caught, '上架失败，请重试'))
    }
  }

  const selling = rows.filter((row) => row.sell && row.modelId)
  return (
    <>
      <PageHeader
        back={{ to: '/channels', label: '我的渠道' }}
        title="添加渠道"
      />
      <Stepper current={step} />
      {step === 0 && (
        <Card title="连接">
          <form className="stack-form channel-connect" noValidate onSubmit={connect}>
            <TextField autoFocus label="名称" maxLength={64} onChange={(event) => setName(event.target.value)} value={name} />
            <TextField
              inputMode="url"
              label="Base URL"
              onChange={(event) => setBaseUrl(event.target.value)}
              placeholder="https://"
              type="url"
              value={baseUrl}
            />
            <PasswordField autoComplete="off" label="上游 Key" onChange={(event) => setApiKey(event.target.value)} value={apiKey} />
            <InlineError>{error}</InlineError>
            <div className="form-actions">
              <Button loading={discover.isPending}>读取模型</Button>
            </div>
          </form>
        </Card>
      )}
      {step === 1 && (
        <Card title="选模型">
          <div className="stack-form">
            <ModelsSection
              onRows={setRows}
              onTest={(modelId) => void runTest(modelId)}
              rows={rows}
              summary={summary}
              testError={testError}
              testing={testing}
              testingModel={testingModel}
            />
            <Disclosure changed={advancedChangedCount(advanced)} title="高级设置">
              <AdvancedFields form={advanced} onChange={setAdvanced} />
            </Disclosure>
            <InlineError>{error}</InlineError>
            <div className="form-actions">
              <Button onClick={() => setStep(0)} type="button" variant="secondary">
                上一步
              </Button>
              <Button onClick={next} type="button">
                下一步
              </Button>
            </div>
          </div>
        </Card>
      )}
      {step === 2 && (
        <Card title="上架">
          <div className="stack-form">
            <dl className="detail-list channel-summary">
              <div>
                <dt>名称</dt>
                <dd>{name}</dd>
              </div>
              <div>
                <dt>Base URL</dt>
                <dd className="mono">{baseUrl}</dd>
              </div>
              <div>
                <dt>卖的模型</dt>
                <dd>{selling.length} 个</dd>
              </div>
              <div>
                <dt>格式</dt>
                <dd>
                  <FormatTags formats={[...new Set(selling.flatMap((row) => row.formats))]} />
                </dd>
              </div>
              <div>
                <dt>高级设置</dt>
                <dd>{advancedChangedCount(advanced) ? `已改 ${advancedChangedCount(advanced)} 项` : '平台默认'}</dd>
              </div>
            </dl>
            <InlineError>{error}</InlineError>
            <div className="form-actions">
              <Button onClick={() => setStep(1)} type="button" variant="secondary">
                上一步
              </Button>
              <Button loading={create.isPending || update.isPending} onClick={() => void publish()} type="button">
                上架
              </Button>
            </div>
          </div>
        </Card>
      )}
    </>
  )
}

function ChannelCalls({ channelId }: { channelId: string }) {
  const [range, setRange] = useState<RangePreset>('today')
  const [outcome, setOutcome] = useState('')
  const params = { from: rangeFrom(range), outcome: outcome || undefined }
  return (
    <Card title="调用">
      <CallExplorer<ChannelCall>
        caption="渠道调用"
        chargedLabel="收入"
        columns={channelCallColumns()}
        listPath={`/api/channels/${channelId}/calls`}
        params={params}
        streamPath={`/api/channels/${channelId}/calls/stream`}
        toolbar={
          <div className="call-filters">
            <Segmented
              label="时间范围"
              onChange={setRange}
              options={[
                { key: 'today', label: '今天' },
                { key: '7d', label: '7 天' },
                { key: '30d', label: '30 天' },
              ]}
              value={range}
            />
            <label className="filter-select">
              <span className="visually-hidden">结果</span>
              <select aria-label="结果" className="input select-input" onChange={(event) => setOutcome(event.target.value)} value={outcome}>
                <option value="">结果：全部</option>
                {outcomeOptions.map((item) => (
                  <option key={item} value={item}>
                    {outcomeLabels[item]}
                  </option>
                ))}
              </select>
            </label>
          </div>
        }
      />
    </Card>
  )
}

/** 编辑：同一结构平铺，另含统计、健康事件、调用与删除。 */
function ChannelEditForm({ detail }: { detail: ChannelDetail }) {
  const navigate = useNavigate()
  const channel = detail.channel
  const [name, setName] = useState(channel.name)
  const [baseUrl, setBaseUrl] = useState(channel.base_url)
  const [apiKey, setApiKey] = useState('')
  const [rows, setRows] = useState<ModelRow[]>(() => rowsFromChannel(channel))
  const [advanced, setAdvanced] = useState<AdvancedForm>(() => advancedFromChannel(channel.advanced))
  const [listed, setListed] = useState(channel.status === 'listed')
  const [summary, setSummary] = useState<{ total: number; matched: number } | null>(null)
  const [error, setError] = useState('')
  const [testError, setTestError] = useState('')
  const [testing, setTesting] = useState(false)
  const [testingModel, setTestingModel] = useState<string | null>(null)
  const [message, setMessage] = useState('')
  const [confirmDelete, setConfirmDelete] = useState(false)
  const discover = useDiscoverChannel()
  const update = useUpdateChannel()
  const test = useTestChannel()
  const remove = useDeleteChannel()

  const body = () => ({
    name: name.trim(),
    base_url: baseUrl.trim(),
    ...(apiKey.trim() ? { api_key: apiKey.trim() } : {}),
    ...(channel.status === 'suspended' ? {} : { status: listed ? ('listed' as const) : ('unlisted' as const) }),
    models: modelsInput(rows),
    advanced: advancedInput(advanced),
  })

  const save = (event: FormEvent) => {
    event.preventDefault()
    setMessage('')
    const problem = (!name.trim() || !baseUrl.trim() ? '请填写名称与 Base URL' : '') || validateRows(rows) || validateAdvanced(advanced)
    setError(problem)
    if (problem) return
    update.mutate(
      { id: channel.id, body: body() },
      {
        onSuccess: () => {
          setApiKey('')
          setMessage('已保存')
        },
        onError: (caught) => setError(errorMessage(caught, '保存失败，请重试')),
      },
    )
  }

  const rediscover = () => {
    setError('')
    discover.mutate(
      { base_url: baseUrl.trim(), api_key: apiKey.trim() },
      {
        onSuccess: (result) => {
          setSummary(discoverSummary(result.upstream_models))
          const known = new Set(rows.map((row) => row.upstream))
          setRows([...rows, ...rowsFromDiscover(result.upstream_models.filter((model) => !known.has(model.id))).map((row) => ({ ...row, sell: false }))])
        },
        onError: (caught) => setError(errorMessage(caught, '读取上游模型失败')),
      },
    )
  }

  const runTest = async (modelId?: string) => {
    if (testing) return
    setTestError('')
    const problem = validateRows(rows)
    if (problem) {
      setTestError(problem)
      return
    }
    setTesting(true)
    setTestingModel(modelId ?? null)
    try {
      await update.mutateAsync({ id: channel.id, body: body() })
      const result = await test.mutateAsync({
        id: channel.id,
        body: { model_ids: modelId ? [modelId] : rows.filter((row) => row.sell && row.modelId).map((row) => row.modelId), apply: false },
      })
      setRows((current) => applyTestResults(current, result.results))
    } catch (caught) {
      setTestError(errorMessage(caught, '测试失败，请重试'))
    } finally {
      setTesting(false)
    }
  }

  return (
    <>
      <PageHeader
        back={{ to: '/channels', label: '我的渠道' }}
        title={channel.name}
      />
      {channel.status === 'suspended' && (
        <div className="notice notice-danger" role="alert">
          被管理员下架{channel.suspended_reason ? `：${channel.suspended_reason}` : ''}
        </div>
      )}
      <form className="channel-edit" noValidate onSubmit={save}>
        <Card title="连接">
          <div className="stack-form">
            <div className="field-row">
              <TextField label="名称" maxLength={64} onChange={(event) => setName(event.target.value)} value={name} />
              <TextField inputMode="url" label="Base URL" onChange={(event) => setBaseUrl(event.target.value)} type="url" value={baseUrl} />
            </div>
            <PasswordField
              autoComplete="off"
              hint="已保存，留空不修改"
              label="上游 Key"
              onChange={(event) => setApiKey(event.target.value)}
              value={apiKey}
            />
            <div>
              <Button disabled={!apiKey.trim()} loading={discover.isPending} onClick={rediscover} size="sm" type="button" variant="secondary">
                重新读取模型
              </Button>
            </div>
          </div>
        </Card>
        <Card title="模型">
          <ModelsSection
            onRows={setRows}
            onTest={(modelId) => void runTest(modelId)}
            rows={rows}
            summary={summary}
            testError={testError}
            testing={testing}
            testingModel={testingModel}
          />
        </Card>
        <Disclosure changed={advancedChangedCount(advanced)} title="高级设置">
          <AdvancedFields form={advanced} onChange={setAdvanced} />
        </Disclosure>
        <InlineError>{error}</InlineError>
        <SuccessMessage>{message}</SuccessMessage>
        <div className="form-actions channel-edit-actions">
          <Switch
            checked={listed}
            disabled={channel.status === 'suspended'}
            label={listed ? '上架' : '下架'}
            onChange={setListed}
          />
          <Button loading={update.isPending && !test.isPending}>保存</Button>
        </div>
      </form>
      <ChannelStatsPanel channelId={channel.id} />
      <Card title="健康事件">
        <ChannelEvents events={detail.events} />
      </Card>
      <ChannelCalls channelId={channel.id} />
      <Card className="danger-zone" title="删除渠道">
        <div className="danger-zone-body">
          <span className="muted-copy">删除后不再接收调用，历史记录保留</span>
          <Button icon={<Icon name="trash" />} onClick={() => setConfirmDelete(true)} type="button" variant="danger">
            删除
          </Button>
        </div>
      </Card>
      <ConfirmDialog
        busy={remove.isPending}
        confirmLabel="删除"
        danger
        error={remove.isError ? errorMessage(remove.error, '删除失败，请重试') : ''}
        onClose={() => setConfirmDelete(false)}
        onConfirm={() => remove.mutate(channel.id, { onSuccess: () => navigate('/channels', { replace: true }) })}
        open={confirmDelete}
        title={`删除 ${channel.name}？`}
      />
    </>
  )
}

function ChannelEdit({ id }: { id: string }) {
  const channel = useChannel(id)
  return (
    <QueryBoundary errorFallback="渠道加载失败" query={channel}>
      {(detail) => <ChannelEditForm detail={detail} key={detail.channel.id} />}
    </QueryBoundary>
  )
}

export function ChannelEditorPage() {
  const { id } = useParams()
  return id ? <ChannelEdit id={id} /> : <ChannelWizard />
}

