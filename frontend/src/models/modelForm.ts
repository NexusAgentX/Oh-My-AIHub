import type { CatalogModel, ModelInput } from '../api/contracts'

export type ModelForm = {
  id: string
  name: string
  provider: string
  contextWindow: string
  parameterInfo: string
  inputModalities: string[]
  outputModalities: string[]
  supportsTools: boolean
  supportsStructuredOutput: boolean
  supportsVision: boolean
  inputPrice: string
  outputPrice: string
  cacheWritePrice: string
  cacheReadPrice: string
  status: 'active' | 'disabled'
}

export const emptyModelForm: ModelForm = {
  id: '',
  name: '',
  provider: '',
  contextWindow: '',
  parameterInfo: '',
  inputModalities: ['text'],
  outputModalities: ['text'],
  supportsTools: false,
  supportsStructuredOutput: false,
  supportsVision: false,
  inputPrice: '0',
  outputPrice: '0',
  cacheWritePrice: '0',
  cacheReadPrice: '0',
  status: 'active',
}

export function modelToForm(model: CatalogModel): ModelForm {
  return {
    id: model.id,
    name: model.name,
    provider: model.provider,
    contextWindow: String(model.context_window),
    parameterInfo: model.parameter_info,
    inputModalities: model.input_modalities,
    outputModalities: model.output_modalities,
    supportsTools: model.supports_tools,
    supportsStructuredOutput: model.supports_structured_output,
    supportsVision: model.supports_vision,
    inputPrice: model.input_price,
    outputPrice: model.output_price,
    cacheWritePrice: model.cache_write_price,
    cacheReadPrice: model.cache_read_price,
    status: model.status,
  }
}

const modelIDPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]*(?:\/[A-Za-z0-9][A-Za-z0-9._:-]*)*$/
const pricePattern = /^(0|[1-9][0-9]{0,5})(\.[0-9]{1,9})?$/

export function validateModelForm(form: ModelForm): Record<string, string> {
  const errors: Record<string, string> = {}
  const modelID = form.id.trim()
  if (modelID.length > 128 || !modelIDPattern.test(modelID)) errors.id = '请输入有效的模型 ID'
  if (!form.name.trim()) errors.name = '请输入模型名称'
  if (!form.provider.trim()) errors.provider = '请输入官方提供商'
  if (!/^\d+$/.test(form.contextWindow) || Number(form.contextWindow) <= 0) {
    errors.contextWindow = '请输入大于 0 的整数'
  }
  if (form.inputModalities.length === 0) errors.inputModalities = '至少选择一种输入模态'
  if (form.outputModalities.length === 0) errors.outputModalities = '至少选择一种输出模态'
  for (const [key, value] of [
    ['inputPrice', form.inputPrice],
    ['outputPrice', form.outputPrice],
    ['cacheWritePrice', form.cacheWritePrice],
    ['cacheReadPrice', form.cacheReadPrice],
  ] as const) {
    if (!pricePattern.test(value.trim()) || Number(value) > 100_000) {
      errors[key] = '请输入 0–100000，最多 9 位小数'
    }
  }
  return errors
}

export function formToModelInput(form: ModelForm): ModelInput {
  return {
    id: form.id.trim(),
    name: form.name.trim(),
    provider: form.provider.trim(),
    context_window: Number(form.contextWindow),
    parameter_info: form.parameterInfo.trim(),
    input_modalities: [...new Set(form.inputModalities)].sort(),
    output_modalities: [...new Set(form.outputModalities)].sort(),
    supports_tools: form.supportsTools,
    supports_structured_output: form.supportsStructuredOutput,
    supports_vision: form.supportsVision,
    input_price: form.inputPrice.trim(),
    output_price: form.outputPrice.trim(),
    cache_write_price: form.cacheWritePrice.trim(),
    cache_read_price: form.cacheReadPrice.trim(),
    status: form.status,
  }
}

export function modelStatusUpdate(
  model: CatalogModel,
  status: ModelForm['status'],
): Omit<ModelInput, 'id'> {
  const { id: _id, ...update } = formToModelInput({
    ...modelToForm(model),
    status,
  })
  return update
}

export function modelFormHasChanges(
  form: ModelForm,
  persisted: CatalogModel | null,
) {
  const baseline = persisted ? modelToForm(persisted) : emptyModelForm
  return JSON.stringify(form) !== JSON.stringify(baseline)
}

export function mergeModelDraft(
  baseline: CatalogModel,
  draft: ModelForm,
  latest: CatalogModel,
): ModelForm {
  const baselineForm = modelToForm(baseline)
  const latestForm = modelToForm(latest)
  const merged = Object.fromEntries(
    (Object.keys(latestForm) as Array<keyof ModelForm>).map((key) => [
      key,
      modelFormValueEquals(draft[key], baselineForm[key])
        ? latestForm[key]
        : draft[key],
    ]),
  ) as ModelForm

  // ID is immutable and status has its own immediate action, so neither should
  // be revived from a stale edit form during a conflict merge.
  return { ...merged, id: latestForm.id, status: latestForm.status }
}

function modelFormValueEquals(
  left: ModelForm[keyof ModelForm],
  right: ModelForm[keyof ModelForm],
) {
  if (Array.isArray(left) && Array.isArray(right)) {
    return [...left].sort().join('\u0000') === [...right].sort().join('\u0000')
  }
  return left === right
}
