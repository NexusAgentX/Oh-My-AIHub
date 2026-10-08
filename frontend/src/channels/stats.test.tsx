import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { RecentFailures, StatusCodes } from './ChannelStatsPanel'

describe('channel stats failures', () => {
  it('shows failures by status code, with no response as its own bucket', () => {
    const markup = renderToStaticMarkup(
      <StatusCodes
        codes={[
          { status_code: 429, count: 5 },
          { status_code: null, count: 2 },
        ]}
      />,
    )
    expect(markup).toContain('429')
    expect(markup).toContain('5 次')
    expect(markup).toContain('无响应')
    expect(renderToStaticMarkup(<StatusCodes codes={[]} />)).toContain('没有失败')
  })

  it('lists recent failures with the raw upstream error', () => {
    const markup = renderToStaticMarkup(
      <RecentFailures
        failures={[
          {
            call_id: 'a',
            created_at: '2026-10-08T08:00:00Z',
            model_id: 'gpt-x',
            status_code: 502,
            error_code: 'upstream_error',
            error_message: '{"error":"bad gateway"}',
            end_reason: 'upstream_error',
          },
          {
            call_id: 'b',
            created_at: '2026-10-08T07:00:00Z',
            model_id: null,
            status_code: null,
            error_code: 'connect_failed',
            error_message: null,
            end_reason: 'connect_failed',
          },
        ]}
      />,
    )
    expect(markup).toContain('502 · upstream_error')
    expect(markup).toContain('{&quot;error&quot;:&quot;bad gateway&quot;}')
    expect(markup).toContain('无响应 · connect_failed')
  })
})
