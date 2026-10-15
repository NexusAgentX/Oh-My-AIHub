import { describe, expect, it } from 'vitest'
import { mapPoint, replayDuration, replaySpans } from './network'

import { networkFixture } from './network.fixture'
describe('request replay measurements', () => {
  it('aligns attempt phases to the call and preserves overlap without adding aggregates', () => {
    const call = networkFixture(), spans = replaySpans(call, 1)
    expect(spans.find(s => s.label === '发送请求')).toMatchObject({ start: 252, end: 255 })
    expect(spans.find(s => s.label === '持续接收与转发')).toMatchObject({ start: 1000, end: 3950 })
    expect(spans.some(s => s.label === '路由')).toBe(false)
    expect(replayDuration(call, spans)).toBe(4000)
    expect(replaySpans(call, 0).some(s => s.side === 'response')).toBe(false)
  })
  it('does not invent positions, missing offsets or client network time', () => {
    const call = networkFixture(); call.attempts[1].offset_ms = null
    expect(replaySpans(call, 1).every(s => s.side === 'gateway')).toBe(true)
    expect(mapPoint(null, 1)).toEqual({ x: 360, y: 382 })
    expect(mapPoint({ latitude: 0, longitude: 0 }, 0)).toEqual({ x: 360, y: 180 })
    call.diagnostics = null
    expect(replaySpans(call, 1)).toEqual([])
  })
})
