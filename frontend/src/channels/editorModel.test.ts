import { describe, expect, it } from 'vitest'
import type { Channel, ChannelOffer, ChannelProtocol } from '../api/types'
import { channelGroups, createInput, rebaseChannelFields, rebaseGroups, validateDraft } from './editorModel'

function offer(
  id: string,
  protocol: ChannelProtocol,
  upstreamModelID = 'provider/model',
  multiplier = '1',
): ChannelOffer {
  return {
    id,
    model_id: 'provider/model',
    model_name: 'Model',
    model_provider: 'Provider',
    protocol,
    upstream_model_id: upstreamModelID,
    multiplier,
    status: 'active',
    validation_version: 1,
    version: 1,
    eligible: false,
    ineligible_reason: 'validation_required',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    price_tiers: [],
    call_success_rate: null,
    ttft_milliseconds: null,
    tokens_per_second: null,
    call_count: null,
    provider_income: null,
    latest_validation: null,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  }
}

function channel(offers: ChannelOffer[]): Channel {
  return {
    id: 'channel-id',
    owner_account_id: 'owner-id',
    owner_display_name: 'Owner',
    display_name: 'Channel',
    base_url: 'https://relay.example.com',
    credential_configured: true,
    credential_version: 1,
    credential_updated_at: null,
    status: 'draft',
    version: 1,
    offers,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  }
}

describe('channel editor conflict rebase', () => {
  it('adopts untouched remote channel fields and preserves touched local fields', () => {
    const latest = {
      ...channel([]),
      display_name: 'Remote name',
      base_url: 'https://remote.example.com',
    }

    expect(rebaseChannelFields({
      displayName: 'Old name',
      displayNameTouched: false,
      baseURL: 'https://old.example.com',
      baseURLTouched: false,
    }, latest)).toMatchObject({
      displayName: 'Remote name',
      baseURL: 'https://remote.example.com',
    })

    expect(rebaseChannelFields({
      displayName: 'Local name',
      displayNameTouched: true,
      baseURL: 'https://local.example.com',
      baseURLTouched: true,
    }, latest)).toMatchObject({
      displayName: 'Local name',
      baseURL: 'https://local.example.com',
    })
  })

  it('adopts a concurrently added protocol unless the user touched its selection', () => {
    const drafts = channelGroups(channel([
      offer('chat', 'openai_chat_completions'),
    ]))
    const rebased = rebaseGroups(drafts, channel([
      offer('chat', 'openai_chat_completions'),
      offer('responses', 'openai_responses', 'provider/responses'),
    ]))

    expect(rebased[0].offers.openai_responses).toMatchObject({
      id: 'responses',
      selected: true,
      selectionTouched: false,
      upstreamModelID: 'provider/responses',
    })
  })

  it('preserves an explicit local cancellation of a concurrently added protocol', () => {
    const drafts = channelGroups(channel([
      offer('chat', 'openai_chat_completions'),
    ]))
    drafts[0].offers.openai_responses.selectionTouched = true
    drafts[0].offers.openai_responses.selected = false

    const rebased = rebaseGroups(drafts, channel([
      offer('chat', 'openai_chat_completions'),
      offer('responses', 'openai_responses'),
    ]))

    expect(rebased[0].offers.openai_responses).toMatchObject({
      id: 'responses',
      selected: false,
      selectionTouched: true,
    })
  })

  it('adopts untouched remote fields and preserves touched local fields', () => {
    const drafts = channelGroups(channel([
      offer('chat', 'openai_chat_completions', 'provider/old', '1'),
    ]))
    const untouched = rebaseGroups(drafts, channel([
      offer('chat', 'openai_chat_completions', 'provider/remote', '2'),
    ]))
    expect(untouched[0]).toMatchObject({ multiplier: '2' })
    expect(untouched[0].offers.openai_chat_completions.upstreamModelID).toBe('provider/remote')

    drafts[0].multiplier = '3'
    drafts[0].multiplierTouched = true
    drafts[0].offers.openai_chat_completions.upstreamModelID = 'provider/local'
    drafts[0].offers.openai_chat_completions.upstreamTouched = true
    const touched = rebaseGroups(drafts, channel([
      offer('chat', 'openai_chat_completions', 'provider/remote', '2'),
    ]))
    expect(touched[0]).toMatchObject({ multiplier: '3' })
    expect(touched[0].offers.openai_chat_completions.upstreamModelID).toBe('provider/local')
  })
})

describe('validateDraft', () => {
  const base = () => {
    const groups = channelGroups(channel([offer('chat', 'openai_chat_completions')]))
    return { displayName: 'Relay', baseURL: 'https://relay.example.com', credential: 'sk-test', groups }
  }

  it('accepts a complete draft', () => {
    expect(validateDraft(base(), false)).toBe('')
  })

  it('requires a credential only when creating', () => {
    const draft = { ...base(), credential: '' }
    expect(validateDraft(draft, false)).toBe('请输入上游 API Key')
    expect(validateDraft(draft, true)).toBe('')
  })

  it('requires at least one enabled protocol', () => {
    const draft = base()
    draft.groups[0].offers.openai_chat_completions.selected = false
    expect(validateDraft(draft, false)).toBe('至少启用一个模型协议')
  })

  it('rejects an invalid multiplier and a missing upstream model id', () => {
    const draft = base()
    draft.groups[0].multiplier = '1001'
    expect(validateDraft(draft, false)).toBe('Model 的倍率无效')
    draft.groups[0].multiplier = ''
    expect(validateDraft(draft, false)).toBe('Model 的倍率无效')
    draft.groups[0].multiplier = '1'
    draft.groups[0].offers.openai_chat_completions.upstreamModelID = ' '
    expect(validateDraft(draft, false)).toBe('Model 缺少上游模型 ID')
  })
})

describe('createInput', () => {
  it('sends only selected protocols with trimmed values', () => {
    const groups = channelGroups(channel([offer('chat', 'openai_chat_completions')]))
    groups[0].offers.openai_responses.upstreamModelID = 'unused'
    expect(createInput({ displayName: ' Relay ', baseURL: ' https://relay.example.com ', credential: 'k', groups })).toEqual({
      display_name: 'Relay',
      base_url: 'https://relay.example.com',
      credential: 'k',
      offers: [{ model_id: 'provider/model', protocol: 'openai_chat_completions', upstream_model_id: 'provider/model', multiplier: '1' }],
    })
  })
})
