import { describe, expect, it } from 'vitest'
import type { CatalogModel } from '../api/contracts'
import {
  emptyModelForm,
  emptyTierForm,
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
      price_tiers: [
        {
          name: '工作日高峰',
          timezone: 'Asia/Shanghai',
          min_prompt_tokens: null,
          max_prompt_tokens: null,
          weekdays: [1, 2, 3, 4, 5],
          start_minute_of_day: 540,
          end_minute_of_day: 720,
          input_price: '2.5',
          output_price: '20',
          cache_write_price: '0',
          cache_read_price: '0.25',
          price_unit: 'points_per_million_tokens',
        },
      ],
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
      price_tiers: [],
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

  it('round-trips persisted price tiers into the form and back', () => {
    const persisted: CatalogModel = {
      id: 'deepseek/deepseek-v4-flash',
      name: 'DeepSeek V4 Flash',
      provider: 'DeepSeek',
      context_window: 131072,
      parameter_info: '',
      input_modalities: ['text'],
      output_modalities: ['text'],
      supports_tools: false,
      supports_structured_output: false,
      supports_vision: false,
      input_price: '1.5',
      output_price: '4.5',
      cache_write_price: '1.5',
      cache_read_price: '0.075',
      price_tiers: [
        {
          name: '上午高峰',
          timezone: 'Asia/Shanghai',
          min_prompt_tokens: null,
          max_prompt_tokens: null,
          weekdays: [1, 2, 3, 4, 5],
          start_minute_of_day: 540,
          end_minute_of_day: 720,
          input_price: '3',
          output_price: '9',
          cache_write_price: '3',
          cache_read_price: '0.15',
          price_unit: 'points_per_million_tokens',
        },
        {
          name: '长上下文',
          timezone: 'UTC',
          min_prompt_tokens: 131072,
          max_prompt_tokens: null,
          weekdays: null,
          start_minute_of_day: null,
          end_minute_of_day: null,
          input_price: '2',
          output_price: '6',
          cache_write_price: '2',
          cache_read_price: '0.1',
          price_unit: 'points_per_million_tokens',
        },
      ],
      price_unit: 'points_per_million_tokens',
      status: 'active',
      version: 1,
      created_at: '2026-09-03T00:00:00Z',
      updated_at: '2026-09-03T00:00:00Z',
      price_updated_at: '2026-09-03T00:00:00Z',
    }

    const form = modelToForm(persisted)
    expect(validateModelForm(form)).toEqual({})
    expect(form.tiers).toHaveLength(2)
    expect(formToModelInput(form).price_tiers).toEqual(persisted.price_tiers.map(
      ({ price_unit: _unit, ...tier }) => tier,
    ))
    expect(modelFormHasChanges(form, persisted)).toBe(false)
  })

  it('rejects tiers without predicates, inverted token ranges and invalid windows', () => {
    const errors = validateModelForm({
      ...emptyModelForm,
      tiers: [
        { ...emptyTierForm, inputPrice: '3' },
        { ...emptyTierForm, minPromptTokens: '500', maxPromptTokens: '200' },
        { ...emptyTierForm, useWindow: true, startTime: '9am', endTime: '12:00' },
        { ...emptyTierForm, weekdays: [1], timezone: 'Not/AZone' },
      ],
    })
    expect(errors['tiers.0.minPromptTokens']).toBeTruthy()
    expect(errors['tiers.1.minPromptTokens']).toBeTruthy()
    expect(errors['tiers.2.startTime']).toBeTruthy()
    expect(errors['tiers.3.timezone']).toBeTruthy()
  })

  it('accepts a cross-midnight window and a weekday-only tier', () => {
    const errors = validateModelForm({
      ...emptyModelForm,
      id: 'deepseek/v4-flash',
      name: 'DeepSeek V4 Flash',
      provider: 'DeepSeek',
      contextWindow: '131072',
      tiers: [
        { ...emptyTierForm, useWindow: true, startTime: '22:00', endTime: '06:00', weekdays: [5] },
        { ...emptyTierForm, weekdays: [6, 7] },
      ],
    })
    expect(errors).toEqual({})
  })
})
