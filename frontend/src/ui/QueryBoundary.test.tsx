import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { ApiError } from '../api/client'
import { QueryBoundary } from './QueryBoundary'

describe('QueryBoundary', () => {
  it('shows a recoverable error state with retry for 501 not_implemented', () => {
    const query = {
      data: undefined,
      isPending: false,
      isError: true,
      error: new ApiError(501, 'not_implemented', '该功能尚未实现'),
      refetch: () => undefined,
    }
    const markup = renderToStaticMarkup(<QueryBoundary query={query}>{() => 'content'}</QueryBoundary>)
    expect(markup).toContain('role="alert"')
    expect(markup).toContain('该功能尚未实现')
    expect(markup).toContain('重试')
    expect(markup).not.toContain('content')
  })

  it('keeps showing stale data when a background refresh fails', () => {
    const query = { data: 'cached', isPending: false, isError: true, error: new Error('x'), refetch: () => undefined }
    expect(renderToStaticMarkup(<QueryBoundary query={query}>{(data) => data}</QueryBoundary>)).toBe('cached')
  })
})
