import { describe, expect, it, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import config, { sanitizeProxyErrorResponse } from './vite.config'

const gatewayPaths = [...readFileSync(new URL('../backend/api/openapi.yaml', import.meta.url), 'utf8').matchAll(/^  (\/\S+):$/gm)]
	.map((match) => match[1])
	.filter((path) => /^\/(?:v1(?:beta)?|backend-api)\//.test(path))

describe('gateway proxy log safety', () => {
	it('disables Nginx request and error logging for every external gateway prefix', () => {
		const nginx = readFileSync(new URL('./nginx.conf', import.meta.url), 'utf8')
		expect(nginx).not.toContain('log_format gateway_safe')
		expect(nginx).toContain('access_log off;')
		expect(nginx).toContain('error_log /dev/null emerg;')
		const namespace = nginx.match(/location ~\* (.+) \{/)
		expect(namespace).not.toBeNull()
		const pattern = new RegExp(namespace![1], 'i')
		for (const path of gatewayPaths) expect(pattern.test(path), path).toBe(true)
	})

	it('proxies every contracted gateway endpoint in the development server', () => {
		// Only stateless inference and model metadata remain; the Codex,
		// resource, cache and token-counting paths are gone.
		expect(gatewayPaths.length).toBeGreaterThan(8)
		const patterns = Object.keys(config.server?.proxy ?? {}).filter((key) => key.startsWith('^')).map((key) => new RegExp(key))
		for (const path of gatewayPaths) expect(patterns.some((pattern) => pattern.test(path)), path).toBe(true)
	})

	it('replaces dynamic path and query secrets in Vite proxy error URLs', () => {
		const response = { req: { url: '/v1beta/models/oma_live_path:generateContent?key=oma_live_query&alt=sse' } }
    sanitizeProxyErrorResponse(response)
		expect(response.req.url).toBe('/v1beta/models/:model')
		expect(response.req.url).not.toContain('oma_live_path')
		expect(response.req.url).not.toContain('oma_live_query')
  })

	it('redacts retired namespace query and path details in proxy errors', () => {
		const response = { req: { url: '/backend-api/unknown/path?session_id=private-session&path=private-note' } }
		sanitizeProxyErrorResponse(response)
		expect(response.req.url).toBe('/backend-api')
	})

  it('registers the sanitizer before Vite can add its default listener', () => {
    const listeners: Array<{ event: string; listener: (...args: any[]) => void }> = []
    const proxy = { on: vi.fn((event: string, listener: (...args: any[]) => void) => listeners.push({ event, listener })) }
		const entry = Object.entries(config.server?.proxy ?? {}).find(([key]) => key.startsWith('^') && new RegExp(key).test('/v1beta/models/example:generateContent'))?.[1]
    expect(entry && typeof entry !== 'string').toBe(true)
    if (!entry || typeof entry === 'string') throw new Error('missing Gemini proxy')
    entry.configure?.(proxy as never, entry as never)
    expect(listeners[0]?.event).toBe('error')
		const response = { req: { url: '/v1beta/models/oma_live_path:generateContent?key=oma_live_query' } }
    listeners[0].listener(new Error('upstream down'), {}, response)
		expect(response.req.url).toBe('/v1beta/models/:model')
  })
})
