import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { DetectionObservations } from './Observations'
import type { Job } from './queries'

type Result = Job['results'][number]
const capacity: NonNullable<Result['capacity']>[number] = {
  kind: 'max_output', unit: 'tokens', declared: 16384,
  settings: { version: 'synthetic', method: 'binary', low: 0, high: 1000, hard_maximum: 1000, precision: 1, precision_mode: 'absolute', rounds: 10, rechecks: 1 },
  parameter_accepted_lower: 512, parameter_rejected_upper: null, verified_lower: 7, proven_failure_upper: null,
  completed: true, stop: 'unconfirmed', steps: [],
}
const fingerprint: NonNullable<Result['fingerprint']> = {
  version: 'synthetic-reference', commit: 'synthetic-commit', detector_sha256: 'synthetic-detector', reference_sha256: 'synthetic-reference', base_sha256: 'synthetic-base', reference_built_at: '2026-10-18', calibration_sha256: 'synthetic-calibration',
  decision: 'unscorable', used_outputs: 2, diagnostics: [], probability_scope: 'unavailable', results: [],
}

describe('detection metadata observations', () => {
  it('keeps declaration, parameter acceptance, measured lower bound and unknown upper separate', () => {
    const html = renderToStaticMarkup(<DetectionObservations result={{ capacity: [capacity] }} />)
    expect(html).toContain('报价声明')
    expect(html).toContain('16,384')
    expect(html).toContain('参数接受的探测点')
    expect(html).toContain('512')
    expect(html).toContain('实测成功下界')
    expect(html).toContain('>7 token<')
    expect(html).toContain('已证实失败上界')
    expect(html).toContain('未知')
    expect(html).toContain('无法继续确认边界')
  })
  it('shows an interrupted search as incomplete', () => {
    const html = renderToStaticMarkup(<DetectionObservations result={{ capacity: [{ ...capacity, completed: false, stop: 'cancelled' }] }} />)
    expect(html).toContain('已取消')
    expect(html).toContain('未完成')
  })
  it('does not invent rankings for fewer than three valid fingerprint samples', () => {
    const html = renderToStaticMarkup(<DetectionObservations result={{ fingerprint }} />)
    expect(html).toContain('有效样本不足，无法评分')
    expect(html).toContain('2/3')
    expect(html).not.toContain('参考库内概率')
  })
  it('presents a bounded reference ranking with explicit identity limits', () => {
    const results = ['one', 'two', 'three', 'four', 'five', 'six'].map((name, i) => ({ model: `fixture-${name}`, display_name: name, family: 'synthetic', family_name: 'synthetic', score: 6 - i, probability: null }))
    const html = renderToStaticMarkup(<DetectionObservations result={{ fingerprint: { ...fingerprint, decision: 'ranked', used_outputs: 3, results } }} />)
    expect(html).toContain('无法据此确定模型身份')
    expect(html).toContain('fixture-one')
    expect(html).toContain('fixture-five')
    expect(html).not.toContain('fixture-six')
    expect(html).toContain('查看全部 6 项')
    expect(html).toContain('未知')
  })
})
