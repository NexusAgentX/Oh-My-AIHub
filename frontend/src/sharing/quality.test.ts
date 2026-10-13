import { expect, it } from 'vitest'
import { qualityMetrics, qualityValue } from './QualityStats'
import type { components } from '../api/schema.gen'
type Values = components['schemas']['DirectoryValues']
it('uses eligible attempts, including failed fallback attempts, as success denominator', () => {
  const metric = qualityMetrics.find((m) => m.key === 'success_rate')!
  expect(
    metric.read({
      attempts: 12,
      counted: 10,
      succeeded: 8,
      excluded: 2,
    } as Values),
  ).toBe(80)
  expect(
    metric.read({
      attempts: 2,
      counted: 0,
      succeeded: 0,
      excluded: 2,
    } as Values),
  ).toBeNull()
})
it('distinguishes zero measurements from missing samples', () => {
  const metric = qualityMetrics.find((m) => m.key === 'ttft_p95')!
  expect(metric.read({ ttft_p95: null } as Values)).toBeNull()
  expect(metric.read({ ttft_p95: 0 } as Values)).toBe(0)
  expect(qualityValue(null, 'ms')).toBe('—')
  expect(qualityValue(0, 'ms')).toBe('0 ms')
})
it('shows aggregate cache ratios without recomputing averages or treating unknown as a miss', () => {
  const cache = qualityMetrics.find((m) => m.key === 'cache_read_ratio')!
  expect(cache.read({ cache_read_ratio: 30, cache_samples: 2 } as Values)).toBe(
    30,
  )
  expect(
    cache.read({ cache_read_ratio: null, cache_samples: 0 } as Values),
  ).toBeNull()
  const hits = qualityMetrics.find((m) => m.key === 'cache_hit_rate')!
  expect(hits.read({ cache_hit_rate: 0, cache_samples: 2 } as Values)).toBe(0)
})
