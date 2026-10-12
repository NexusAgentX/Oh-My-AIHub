import { describe, expect, it } from 'vitest'
import { analysisDrill, metricGroups } from './CallAnalysis'
import { emptyCallFilters, filtersToParams } from './CallFilterBar'
import type { components } from '../api/schema.gen'

type Values = components['schemas']['AnalysisValues']
const values = {calls:10, finished:8, attempted:4,first_succeeded:3,succeeded:4,stream_finished:2,stream_interrupted:1,interrupted:2,disconnected:2,attempts:7,attempt_succeeded:4,retried:2,recovered:1} as Values
const read=(category:string,key:string)=>metricGroups[category].find(m=>m.key===key)!.read(values,60)
describe('analysis denominators and precise drill',()=>{
 it('distinguishes rejected calls, upstream attempts and streaming failures',()=>{
  expect(read('reliability','success')).toBe(50)
  expect(read('reliability','first')).toBe(75)
  expect(read('reliability','retry_rate')).toBe(50)
  expect(read('reliability','recover')).toBe(50)
  expect(read('streaming','interrupt_rate')).toBe(50)
  expect(read('reliability','rpm')).toBe(10)
 })
 it('preserves an empty facet independently of a literal tag',()=>{
  expect(analysisDrill('tag','','stream','false')).toEqual({dimension:'tag',dimension_value:'',second_dimension:'stream',second_value:'false'})
  expect(analysisDrill('tag','__empty__','all','').dimension_value).toBe('__empty__')
 })
 it('keeps explicit false and zero filters and exact time bounds',()=>{
  const p=filtersToParams({...emptyCallFilters,stream:'false',billed:'false',min_tokens:'0',min_attempts:'0',from:'2026-01-01T01:00:00Z',to:'2026-01-02T01:00:00Z'})
  expect(p).toMatchObject({stream:false,billed:false,min_tokens:0,min_attempts:0,from:'2026-01-01T01:00:00Z',to:'2026-01-02T01:00:00Z'})
 })
 it('does not replace missing latency with zero',()=>{
  const metric=metricGroups.latency.find(m=>m.key==='ttft_p95')!
  expect(metric.read({ttft_p95:null} as Values,60)).toBeNull()
  expect(metric.read({ttft_p95:0} as Values,60)).toBe(0)
 })
})
