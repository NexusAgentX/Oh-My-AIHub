import { describe, expect, it } from 'vitest'
import {
  emptyModelForm,
  emptyTier,
  formToTierInput,
  isValidPrice,
  maxPriceTiers,
  moveTier,
  tierConditionSummary,
  tierToForm,
  timeToMinutes,
  validateModelForm,
  validateTier,
  type TierForm,
} from './modelForm'
import type { PriceTierInput } from './types'

const tier = (patch: Partial<TierForm>): TierForm => ({ ...emptyTier(), name: '档', ...patch })

describe('price tier validation (mirrors catalog.validatePriceTiers)', () => {
  it('requires at least one condition', () => {
    expect(validateTier(tier({ useTokens: true })).condition).toBe('至少设置一个条件')
    expect(validateTier(tier({ useTokens: false, useTime: true })).condition).toBe('至少设置一个条件')
  })

  it('accepts a long-context tier with only a lower bound', () => {
    expect(validateTier(tier({ minPromptTokens: '200000' }))).toEqual({})
  })

  it('requires the lower token bound to be below the upper bound', () => {
    expect(validateTier(tier({ minPromptTokens: '200', maxPromptTokens: '200' })).tokens).toBe('下限必须小于上限')
    expect(validateTier(tier({ minPromptTokens: '-1' })).tokens).toBe('token 数为非负整数')
  })

  it('accepts windows that cross midnight and rejects empty ones', () => {
    const night = tier({ useTokens: false, useTime: true, useWindow: true, startTime: '22:00', endTime: '06:00' })
    expect(validateTier(night)).toEqual({})
    expect(formToTierInput(night)).toMatchObject({ start_minute_of_day: 1320, end_minute_of_day: 360 })
    expect(validateTier({ ...night, endTime: '22:00' }).time).toBe('开始与结束不能相同')
    expect(validateTier({ ...night, startTime: '24:00' }).time).toBeTruthy()
    expect(validateTier({ ...night, startTime: '00:00', endTime: '24:00' })).toEqual({})
  })

  it('accepts weekdays alone and validates the timezone', () => {
    const weekend = tier({ useTokens: false, useTime: true, weekdays: [6, 7] })
    expect(validateTier(weekend)).toEqual({})
    expect(validateTier({ ...weekend, timezone: 'Mars/Base' }).timezone).toBe('无效的时区')
  })

  it('bounds the four prices to 0～100000 with at most 9 decimals', () => {
    expect(isValidPrice('100000')).toBe(true)
    expect(isValidPrice('100000.5')).toBe(false)
    expect(isValidPrice('1.123456789')).toBe(true)
    expect(isValidPrice('1.1234567891')).toBe(false)
    expect(
      validateTier(
        tier({ minPromptTokens: '1', prices: { input: '-1', output: '0', cache_read: '0', cache_write: '0' } }),
      ).prices,
    ).toBeTruthy()
  })

  it('only submits enabled conditions', () => {
    const input = formToTierInput(
      tier({
        minPromptTokens: '1000',
        useTime: false,
        weekdays: [1],
        useWindow: true,
        startTime: '01:00',
        endTime: '02:00',
      }),
    )
    expect(input).toMatchObject({
      min_prompt_tokens: 1000,
      weekdays: null,
      start_minute_of_day: null,
      end_minute_of_day: null,
    })
  })

  it('round-trips saved tiers through the form', () => {
    const saved = {
      seq: 1,
      name: '夜间',
      min_prompt_tokens: null,
      max_prompt_tokens: null,
      timezone: 'Asia/Shanghai',
      weekdays: null,
      start_minute_of_day: 30,
      end_minute_of_day: 510,
      prices: { input: '1.5', output: '7.5', cache_read: '0.15', cache_write: '1.875' },
    }
    const { seq: _seq, ...expected } = saved
    expect(formToTierInput(tierToForm(saved))).toEqual(expected)
  })

  it('parses 24:00 as the end of day', () => {
    expect(timeToMinutes('24:00')).toBe(1440)
    expect(timeToMinutes('24:01')).toBeNull()
  })
})

describe('price tier list', () => {
  const tiers = (count: number): PriceTierInput[] =>
    Array.from({ length: count }, (_, index) => ({
      name: `档 ${index + 1}`,
      min_prompt_tokens: index,
      prices: { input: '1', output: '1', cache_read: '1', cache_write: '1' },
    }))

  it('caps a model at 16 tiers', () => {
    expect(maxPriceTiers).toBe(16)
    const form = { ...emptyModelForm(), id: 'gpt-x', displayName: 'GPT X' }
    expect(validateModelForm({ ...form, tiers: tiers(16) }, true).tiers).toBeUndefined()
    expect(validateModelForm({ ...form, tiers: tiers(17) }, true).tiers).toBe('最多 16 档')
  })

  it('moves tiers up and down to change first-match priority', () => {
    const list = ['a', 'b', 'c']
    expect(moveTier(list, 2, -1)).toEqual(['a', 'c', 'b'])
    expect(moveTier(list, 0, 1)).toEqual(['b', 'a', 'c'])
    expect(moveTier(list, 0, -1)).toBe(list)
  })

  it('summarises conditions in one line', () => {
    expect(tierConditionSummary({ min_prompt_tokens: 200000, prices: tiers(1)[0].prices })).toBe('输入侧 ≥ 200k')
    expect(
      tierConditionSummary({
        timezone: 'Asia/Shanghai',
        start_minute_of_day: 30,
        end_minute_of_day: 510,
        prices: tiers(1)[0].prices,
      }),
    ).toBe('每天 00:30–08:30 北京时间')
    expect(
      tierConditionSummary({
        timezone: 'UTC',
        weekdays: [1, 2, 3, 4, 5],
        start_minute_of_day: 1320,
        end_minute_of_day: 360,
        prices: tiers(1)[0].prices,
      }),
    ).toBe('工作日 22:00–次日 06:00 UTC')
  })

  it('rejects model names with slashes or colons', () => {
    const form = { ...emptyModelForm(), displayName: 'x' }
    expect(validateModelForm({ ...form, id: 'org/model' }, true).id).toBeTruthy()
    expect(validateModelForm({ ...form, id: 'gemini:flash' }, true).id).toBeFalsy()
    expect(validateModelForm({ ...form, id: 'claude@20250101' }, true).id).toBeFalsy()
    expect(validateModelForm({ ...form, id: 'claude-sonnet-4.5' }, true).id).toBeUndefined()
    expect(validateModelForm({ ...form, id: '' }, false).id).toBeUndefined()
  })
})

it('keeps explicit zero, omits inherited detail prices and accepts response conditions', () => {
  const form = {
    ...emptyTier(),
    name: '实际服务档',
    useTokens: false,
    serviceTier: 'openai:default',
    prices: {
      input: '1',
      output: '2',
      cache_read: '0',
      cache_write: '0',
      token_prices: { input_audio: '0', output_audio: '' },
    },
  }
  expect(validateTier(form)).toEqual({})
  expect(formToTierInput(form).prices.token_prices).toEqual({ input_audio: '0' })
  expect(formToTierInput(form).service_tier).toBe('openai:default')
})
