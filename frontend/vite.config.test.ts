import { describe, expect, it, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import config, { sanitizeProxyErrorResponse } from './vite.config'

describe('gateway proxy log safety', () => {
	it('disables Nginx request and error logging for every external gateway prefix', () => {
		const nginx = readFileSync(new URL('./nginx.conf', import.meta.url), 'utf8')
		expect(nginx).not.toContain('log_format gateway_safe')
		expect(nginx).toContain('access_log off;')
		expect(nginx).toContain('error_log /dev/null emerg;')
		expect(nginx).toContain('location ~* ^/v1(?:beta)?(?:/|$)')
	})

	it('replaces dynamic path and query secrets in Vite proxy error URLs', () => {
		const response = { req: { url: '/v1beta/models/oma_live_path:generateContent?key=oma_live_query&alt=sse' } }
    sanitizeProxyErrorResponse(response)
		expect(response.req.url).toBe('/v1beta/models/:model')
		expect(response.req.url).not.toContain('oma_live_path')
		expect(response.req.url).not.toContain('oma_live_query')
  })

  it('registers the sanitizer before Vite can add its default listener', () => {
    const listeners: Array<{ event: string; listener: (...args: any[]) => void }> = []
    const proxy = { on: vi.fn((event: string, listener: (...args: any[]) => void) => listeners.push({ event, listener })) }
		const entry = config.server?.proxy?.['^/v1(?:beta)?(?:/|$)']
    expect(entry && typeof entry !== 'string').toBe(true)
    if (!entry || typeof entry === 'string') throw new Error('missing Gemini proxy')
    entry.configure?.(proxy as never, entry as never)
    expect(listeners[0]?.event).toBe('error')
		const response = { req: { url: '/v1beta/models/oma_live_path:generateContent?key=oma_live_query' } }
    listeners[0].listener(new Error('upstream down'), {}, response)
		expect(response.req.url).toBe('/v1beta/models/:model')
  })
})
