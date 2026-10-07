import { describe, expect, it } from 'vitest'
import type { CatalogModel } from '../api/contracts'
import {
  moveTier,
  setTierCondition,
  tierConditionSummary,
  timeToMinutes,
  toggleWeekday,
  validateTierForm,
  emptyModelForm,
  emptyTierForm,
  formToModelInput,
  formToTierInput,
  tierToForm,
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
        { ...emptyTierForm, useTokens: false, useTime: true, useWindow: true, startTime: '9am', endTime: '12:00' },
        { ...emptyTierForm, useTokens: false, useTime: true, weekdays: [1], timezone: 'Not/AZone' },
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
        { ...emptyTierForm, useTokens: false, useTime: true, useWindow: true, startTime: '22:00', endTime: '06:00', weekdays: [5] },
        { ...emptyTierForm, useTokens: false, useTime: true, weekdays: [6, 7] },
      ],
    })
    expect(errors).toEqual({})
  })
})

describe('conditional tier editing', () => {
  const peak = {
    ...emptyTierForm,
    name: '工作日高峰',
    useTokens: false,
    useTime: true,
    timezone: 'Asia/Shanghai',
    weekdays: [1, 2, 3, 4, 5],
    useWindow: true,
    startTime: '09:00',
    endTime: '18:00',
    inputPrice: '2',
  }

  it('only submits conditions that are switched on', () => {
    const withLeftovers = { ...peak, minPromptTokens: '1000', maxPromptTokens: '2000' }
    expect(formToTierInput(withLeftovers)).toMatchObject({
      min_prompt_tokens: null,
      max_prompt_tokens: null,
      weekdays: [1, 2, 3, 4, 5],
      start_minute_of_day: 540,
      end_minute_of_day: 1080,
    })
    const tokensOnly = { ...peak, useTokens: true, useTime: false, minPromptTokens: '200000' }
    expect(formToTierInput(tokensOnly)).toMatchObject({
      min_prompt_tokens: 200000,
      max_prompt_tokens: null,
      weekdays: null,
      start_minute_of_day: null,
      end_minute_of_day: null,
    })
  })

  it('clears fields when a condition is switched off', () => {
    const cleared = setTierCondition({ ...peak, useTokens: true, minPromptTokens: '10' }, 'tokens', false)
    expect(cleared).toMatchObject({ useTokens: false, minPromptTokens: '', maxPromptTokens: '' })
    const noTime = setTierCondition(peak, 'time', false)
    expect(noTime).toMatchObject({ useTime: false, weekdays: [], useWindow: false, startTime: '', endTime: '' })
    expect(setTierCondition(noTime, 'time', true).useTime).toBe(true)
  })

  it('derives the switches from persisted tiers', () => {
    const form = tierToForm({
      name: '',
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
    })
    expect(form).toMatchObject({ useTokens: true, useTime: false })
    expect(tierConditionSummary(form)).toBe('输入 ≥ 131,072')
  })

  it('summarizes the active conditions with the timezone', () => {
    expect(tierConditionSummary(peak)).toBe('工作日 09:00–18:00（Asia/Shanghai）')
    expect(tierConditionSummary({ ...peak, useTime: false })).toBe('未设置条件')
    expect(
      tierConditionSummary({ ...emptyTierForm, minPromptTokens: '32000', maxPromptTokens: '200000' }),
    ).toBe('输入 32K–200K')
  })

  it('validates a single tier and requires at least one active condition', () => {
    expect(validateTierForm(peak)).toEqual({})
    expect(validateTierForm({ ...peak, useTime: false })['tier.conditions']).toBeTruthy()
    expect(validateTierForm({ ...peak, useWindow: false, weekdays: [] })['tier.weekdays']).toBeTruthy()
    expect(validateTierForm({ ...emptyTierForm })['tier.minPromptTokens']).toBeTruthy()
    expect(validateTierForm({ ...peak, inputPrice: '-1' })['tier.inputPrice']).toBeTruthy()
  })

  it('accepts a window ending at midnight and keeps it round-tripping', () => {
    expect(timeToMinutes('24:00')).toBe(1440)
    const evening = { ...peak, startTime: '18:00', endTime: '24:00' }
    expect(validateTierForm(evening)).toEqual({})
    expect(formToTierInput(evening).end_minute_of_day).toBe(1440)
    expect(validateTierForm({ ...peak, startTime: '24:00' })['tier.startTime']).toBeTruthy()
  })

  it('reorders tiers without mutating the input and ignores out-of-range moves', () => {
    const tiers = ['a', 'b', 'c']
    expect(moveTier(tiers, 1, -1)).toEqual(['b', 'a', 'c'])
    expect(moveTier(tiers, 1, 1)).toEqual(['a', 'c', 'b'])
    expect(moveTier(tiers, 0, -1)).toBe(tiers)
    expect(moveTier(tiers, 2, 1)).toBe(tiers)
    expect(tiers).toEqual(['a', 'b', 'c'])
  })

  it('keeps weekdays unique and sorted', () => {
    const added = toggleWeekday(toggleWeekday({ ...emptyTierForm, weekdays: [5] }, 2, true), 5, true)
    expect(added.weekdays).toEqual([2, 5])
    expect(toggleWeekday(added, 2, false).weekdays).toEqual([5])
  })
})
