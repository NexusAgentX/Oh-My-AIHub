import { describe, expect, it } from 'vitest'
import { createLatestRequestGate } from './requestGate'

describe('latest route request gate', () => {
  it('rejects an older response after a newer route request begins', () => {
    const gate = createLatestRequestGate()
    const oldRoute = gate.begin()
    const newRoute = gate.begin()

    expect(gate.isCurrent(oldRoute)).toBe(false)
    expect(gate.isCurrent(newRoute)).toBe(true)
  })

  it('rejects an in-flight response after the route unmounts', () => {
    const gate = createLatestRequestGate()
    const request = gate.begin()
    gate.invalidate()

    expect(gate.isCurrent(request)).toBe(false)
  })
})
