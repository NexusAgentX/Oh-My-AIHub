import { defineConfig, type ProxyOptions } from 'vite'
import react from '@vitejs/plugin-react'

type ProxyErrorResponse = { req?: { url?: string } }
export function sanitizeProxyErrorResponse(response: ProxyErrorResponse) {
  const requestURL = response.req?.url
  if (!requestURL) return

	let pathname = ''
  try {
		pathname = new URL(requestURL, 'http://vite-proxy.invalid').pathname
  } catch {
		pathname = requestURL.split('?', 1)[0]
  }
	response.req!.url = gatewayRouteLabel(pathname)
}

export function gatewayRouteLabel(pathname: string) {
	if (pathname === '/v1/chat/completions') return '/v1/chat/completions'
	if (pathname === '/v1/responses') return '/v1/responses'
	if (pathname === '/v1/messages') return '/v1/messages'
	if (pathname.startsWith('/v1beta/models/')) return '/v1beta/models/:model'
	if (pathname === '/api' || pathname.startsWith('/api/')) return '/api'
	return '/proxy'
}

const backendProxy: ProxyOptions = {
  target: 'http://127.0.0.1:8080',
  changeOrigin: false,
  timeout: 30 * 60 * 1000,
  proxyTimeout: 30 * 60 * 1000,
  configure(proxy) {
    // Vite registers its default error logger after configure(). Sanitize only
    // the error-path URL before that logger sees it; normal proxy requests keep
    // their query so the Go handler can reject invalid `key` parameters.
		proxy.on('error', (_error, _request, response) => {
			sanitizeProxyErrorResponse(response as ProxyErrorResponse)
    })
    proxy.on('proxyReq', (proxyRequest, request) => {
      if (request.headers.host) {
        proxyRequest.setHeader('Host', request.headers.host)
      }
    })
  },
}

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': { ...backendProxy },
			'^/v1(?:beta)?(?:/|$)': { ...backendProxy },
    },
  },
})
