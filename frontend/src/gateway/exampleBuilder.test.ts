import { describe, expect, it } from 'vitest'
import { protocols } from './presentation'
import {
  buildCallExample,
  protocolEndpoint,
  protocolHeaders,
  type ExampleLanguage,
} from './exampleBuilder'

const base = { baseURL: 'https://hub.example.com/', modelID: 'openai/gpt-5', apiKey: 'sk-test' }

describe('call examples', () => {
  it('maps every native protocol to its external entry', () => {
    expect(protocolEndpoint('openai_chat_completions', 'm', base.baseURL)).toBe(
      'https://hub.example.com/v1/chat/completions',
    )
    expect(protocolEndpoint('openai_responses', 'm', base.baseURL)).toBe(
      'https://hub.example.com/v1/responses',
    )
    expect(protocolEndpoint('anthropic_messages', 'm', base.baseURL)).toBe(
      'https://hub.example.com/v1/messages',
    )
    expect(
      protocolEndpoint('google_gemini_generate_content', 'google/gemini-2.5', base.baseURL),
    ).toBe('https://hub.example.com/v1beta/models/google/gemini-2.5:generateContent')
  })

  it('uses each protocol credential header', () => {
    expect(protocolHeaders('openai_responses', 'k')).toMatchObject({ Authorization: 'Bearer k' })
    expect(protocolHeaders('anthropic_messages', 'k')).toMatchObject({ 'x-api-key': 'k' })
    expect(protocolHeaders('google_gemini_generate_content', 'k')).toMatchObject({
      'x-goog-api-key': 'k',
    })
  })

  it.each(protocols.flatMap((protocol) =>
    (['curl', 'python', 'node'] as ExampleLanguage[]).map((language) => [protocol, language] as const),
  ))('embeds the real Base URL and key for %s / %s', (protocol, language) => {
    const text = buildCallExample(language, { ...base, protocol })
    expect(text).toContain('https://hub.example.com/v1')
    expect(text).toContain('sk-test')
    if (protocol !== 'google_gemini_generate_content') {
      expect(text).toContain('"model": "openai/gpt-5"')
    }
  })

  it('produces a valid JSON body in curl', () => {
    const text = buildCallExample('curl', { ...base, protocol: 'anthropic_messages' })
    const body = text.slice(text.indexOf("-d '") + 4, text.lastIndexOf("'"))
    expect(JSON.parse(body)).toMatchObject({ model: 'openai/gpt-5', max_tokens: 256 })
  })
})
