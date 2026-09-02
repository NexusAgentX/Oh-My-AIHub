import { describe, expect, it } from 'vitest'
import type { CatalogModel } from '../api/contracts'
import {
  emptyModelForm,
  formToModelInput,
  modelFormHasChanges,
  mergeModelDraft,
  modelStatusUpdate,
  modelToForm,
  validateModelForm,
} from './modelForm'

describe('model form mapping', () => {
  it('preserves exact decimal strings and normalizes modalities', () => {
    const form = {
      ...emptyModelForm,
      id: 'openai/gpt-5.2',
      name: 'GPT-5.2',
      provider: 'OpenAI',
      contextWindow: '400000',
      inputModalities: ['text', 'image', 'text'],
      outputModalities: ['text'],
      inputPrice: '0.0375',
      outputPrice: '10.125',
      cacheWritePrice: '0',
      cacheReadPrice: '0.000000001',
    }
    expect(validateModelForm(form)).toEqual({})
    expect(formToModelInput(form)).toMatchObject({
      id: 'openai/gpt-5.2',
      context_window: 400000,
      input_modalities: ['image', 'text'],
      input_price: '0.0375',
      cache_read_price: '0.000000001',
    })
  })

  it('rejects invalid IDs, empty modalities and out-of-range prices', () => {
    const errors = validateModelForm({
      ...emptyModelForm,
      id: 'bad id',
      name: '',
      provider: '',
      contextWindow: '1.5',
      inputModalities: [],
      outputModalities: [],
      inputPrice: '0.0000000001',
      outputPrice: '100000.01',
    })
    expect(errors).toMatchObject({
      id: expect.any(String),
      name: expect.any(String),
      provider: expect.any(String),
      contextWindow: expect.any(String),
      inputModalities: expect.any(String),
      outputModalities: expect.any(String),
      inputPrice: expect.any(String),
      outputPrice: expect.any(String),
    })
  })

  it.each([
    'provider//model',
    'provider/./model',
    'provider/../model',
    '/model',
    'provider/model/',
  ])('rejects path-ambiguous model ID %s', (id) => {
    const errors = validateModelForm({
      ...emptyModelForm,
      id,
      name: 'Model',
      provider: 'Provider',
      contextWindow: '1',
    })
    expect(errors.id).toBeTruthy()
  })

  it('builds a status update from the persisted model snapshot', () => {
    const persisted: CatalogModel = {
      id: 'openai/gpt-5.2',
      name: 'GPT-5.2',
      provider: 'OpenAI',
      context_window: 400000,
      parameter_info: '',
      input_modalities: ['text'],
      output_modalities: ['text'],
      supports_tools: true,
      supports_structured_output: true,
      supports_vision: false,
      input_price: '1.25',
      output_price: '10',
      cache_write_price: '0',
      cache_read_price: '0.125',
      price_unit: 'points_per_million_tokens',
      status: 'active',
      version: 3,
      created_at: '2026-09-02T00:00:00Z',
      updated_at: '2026-09-02T00:00:00Z',
      price_updated_at: '2026-09-02T00:00:00Z',
    }

    expect(modelStatusUpdate(persisted, 'disabled')).toMatchObject({
      name: 'GPT-5.2',
      input_price: '1.25',
      status: 'disabled',
    })
    expect(modelFormHasChanges({ ...emptyModelForm }, null)).toBe(false)
    expect(modelFormHasChanges(modelToForm(persisted), persisted)).toBe(false)
    expect(
      modelFormHasChanges(
        { ...modelToForm(persisted), inputPrice: '2' },
        persisted,
      ),
    ).toBe(true)

  })

  it('three-way merges only locally changed fields onto the latest model', () => {
    const baseline: CatalogModel = {
      id: 'community/model',
      name: 'Baseline',
      provider: 'Original Provider',
      context_window: 128000,
      parameter_info: '',
      input_modalities: ['text'],
      output_modalities: ['text'],
      supports_tools: false,
      supports_structured_output: false,
      supports_vision: false,
      input_price: '1',
      output_price: '2',
      cache_write_price: '0',
      cache_read_price: '0',
      price_unit: 'points_per_million_tokens',
      status: 'active',
      version: 1,
      created_at: '2026-09-02T00:00:00Z',
      updated_at: '2026-09-02T00:00:00Z',
      price_updated_at: '2026-09-02T00:00:00Z',
    }
    const draft = {
      ...modelToForm(baseline),
      name: 'Local Name',
      inputModalities: ['image', 'text'],
    }
    const latest: CatalogModel = {
      ...baseline,
      provider: 'Remote Provider',
      output_price: '3',
      status: 'disabled',
      version: 2,
    }

    expect(mergeModelDraft(baseline, draft, latest)).toEqual({
      ...modelToForm(latest),
      name: 'Local Name',
      inputModalities: ['image', 'text'],
    })
  })
})
