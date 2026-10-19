import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import type { components } from '../api/schema.gen'
import { MarketModelsPage, MarketReportPage, PriceText,ModelRoutingCard } from './Pages'

type Schemas=components['schemas']
describe('approved group market presentation',()=>{
  it('keeps unknown, measured zero and self net payment distinct',()=>{
    expect(renderToStaticMarkup(<PriceText value={null}/>)).toBe('未知')
    expect(renderToStaticMarkup(<PriceText value={{amount:'0',unit:'tokens',quantity:1_000_000,self_net_zero:false}}/>)).toContain('0 积分 / M')
    expect(renderToStaticMarkup(<PriceText value={{amount:'0',unit:'',quantity:0,self_net_zero:true}}/>)).toBe('站内净支付 0')
  })
  it('uses ID titles and server-filtered totals without synthesizing an aggregate capability',()=>{
    const client=new QueryClient({defaultOptions:{queries:{staleTime:Infinity,retry:false}}})
    const model:Schemas['MarketBrowseModel']={id:'vendor/model:v1',model_types:['text'],formats:['openai_responses'],protocols:['openai-responses'],online_channels:1,price:null,offers:[{group_model_id:'offering',group_id:'group',supplier_id:'supplier',format:'openai_responses',protocol:'openai-responses',channel_ids:['channel'],online_channel_ids:['channel']}]}
    client.setQueryData(['market','models',''],{pages:[{items:[model],total:1,next_cursor:null,price_sort_fallback:false,suppliers:[],groups:[]}],pageParams:['']})
    client.setQueryData(['market','suppliers'],{items:[],groups:[]})
    const html=renderToStaticMarkup(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/models']}><MarketModelsPage/></MemoryRouter></QueryClientProvider>)
    expect(html).toContain('vendor/model:v1')
    expect(html).toContain('共 1 个模型')
    expect(html).toContain('同一候选同时满足')
    expect(html).toContain('匹配候选的典型费用')
    expect(html).not.toContain('最低参考价')
    expect(html).not.toContain('0 积分')
    client.clear()
  })
  it('separates upstream negatives, unobserved CLI behavior and stale independent layers',()=>{
    const client=new QueryClient({defaultOptions:{queries:{staleTime:Infinity,retry:false}}})
    const common={endpoints:[],reason:'unsupported',current:true,aggregate:false,scope_version:'scope-v1',code_version:'implementation-v1',source_ids:['RL10'],estimated_http:2,usage:{http:2,generate:1,count:0,query:1,cleanup:0,input_tokens:3,output_tokens:4,images:0,tools:0},capacity:null,settings:null,images:null,fingerprint:null}
    const report:Schemas['MarketPublicReport']={id:'report',target_kind:'group_model',target_id:'offering',group_id:'group',state:'finished',validity:'partial',complete:true,manifest_version:'scope-v1',registry_version:'registry-v1',updated_at:'2026-10-19T00:00:00Z',parameters:{context:{version:'v1',method:'binary',low:1,high:1024,hard_maximum:1024,precision_mode:'absolute',precision:1,rounds:4,rechecks:0},max_output:{version:'v1',method:'binary',low:1,high:1024,hard_maximum:1024,precision_mode:'absolute',precision:1,rounds:4,rechecks:0}},usage:common.usage,sources:[{id:'codex-cli',url:'https://github.com/openai/codex',version:'0.163.0'}],exclusions:[],scenarios:[{...common,id:'codex-special.RL10@upstream_protocol',layer:'upstream_protocol',state:'unsupported',assertions:[{id:'a1',label:'native_compact',state:'unsupported',reason:'unsupported'}]},{...common,id:'codex-special.RL10@official_cli',layer:'official_cli',state:'inconclusive',current:false,reason:'client_disabled',assertions:[{id:'a2',label:'async_compaction',state:'unobserved',reason:'not_observed'}]}]}
    client.setQueryData(['market','report','report'],report)
    const html=renderToStaticMarkup(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/reports/report']}><Routes><Route path="/reports/:id" element={<MarketReportPage/>}/></Routes></MemoryRouter></QueryClientProvider>)
    expect(html).toContain('上游协议');expect(html).toContain('官方 CLI')
    expect(html).toContain('不支持');expect(html).toContain('未观测')
    expect(html).toContain('当前证据失效');expect(html).toContain('0.163.0')
    expect(html).not.toContain('277/277')
    client.clear()
  })
  it('edits the complete model route even when a detail page is filtered to one group',()=>{
    const client=new QueryClient({defaultOptions:{queries:{staleTime:Infinity,retry:false}}})
    const model:Schemas['MarketBrowseModel']={id:'model',model_types:['text'],formats:['openai_responses'],protocols:['openai-responses'],online_channels:2,price:null,offers:[]}
    const preference:Schemas['RoutingPreference']={model_id:'model',source:'account',mode:'manual',order:['hidden-channel','visible-channel'],excluded:['hidden-channel'],updated_at:null,max_attempts:null,ttft_timeout_ms:null,idle_timeout_ms:null,request_timeout_ms:null}
    const channels:Schemas['MarketBrowseRoutingChannel'][]=['hidden-channel','visible-channel'].map(id=>({id,name:id,formats:['openai_responses'],multiplier:'1',is_mine:false,state:'available',cooldown_remaining_seconds:null,routing_metrics:[],typical_price:null}))
    client.setQueryData(['market','model','model',''],{model,routing:preference,routing_channels:channels,offers:[],channels:[],suppliers:[],groups:[]}satisfies Schemas['MarketBrowseDetail'])
    client.setQueryData(['settings','form-defaults'],{default_max_attempts:2,default_ttft_timeout_ms:30000,default_request_timeout_ms:120000,default_idle_timeout_ms:60000})
    const html=renderToStaticMarkup(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/models/model?group_id=filtered-group']}><ModelRoutingCard id="model"/></MemoryRouter></QueryClientProvider>)
    expect(html).toContain('hidden-channel');expect(html).toContain('visible-channel')
    expect(html).toContain('该模型的全部渠道')
    expect(html.indexOf('使用 hidden-channel')).toBeLessThan(html.indexOf('使用 visible-channel'))
    client.clear()
  })
})
