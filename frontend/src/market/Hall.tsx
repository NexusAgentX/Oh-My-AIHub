import { Link, useParams, useSearchParams } from 'react-router-dom'
import type { components } from '../api/schema.gen'
import { FormatTags } from '../calls'
import { MarkdownContent } from '../markdown/MarkdownContent'
import { SharingNav } from '../sharing/components'
import { Badge, Button, ButtonLink, Card, EmptyState, PageHeader, QueryBoundary, SearchInput, TextField, Toolbar } from '../ui'
import { useMarketChannel, useMarketHall } from './queries'

type Channel=components['schemas']['MarketHallChannel']
function Parents({channel}:{channel:Channel}){
  return <div className="sharing-line"><Link to={`/suppliers/${channel.supplier_id}`}>{channel.supplier_name}</Link><Link to={`/groups/${channel.group_id}`}>{channel.group_name}</Link>{channel.is_mine&&<Badge tone="accent">我的渠道</Badge>}</div>
}
export function MarketHallPage(){
  const [params,setParams]=useSearchParams(),query=useMarketHall(params)
  const update=(key:string,value:string)=>setParams(current=>{const next=new URLSearchParams(current);if(value)next.set(key,value);else next.delete(key);next.delete('cursor');return next},{replace:true})
  return <><PageHeader title="渠道大厅" actions={<ButtonLink to="/channels/new">共享渠道</ButtonLink>}/><SharingNav/>
    <Toolbar><SearchInput label="搜索渠道" value={params.get('channel_q')??''} onChange={e=>update('channel_q',e.target.value)}/><TextField label="模型 ID（精确）" value={params.get('model_id')??''} onChange={e=>update('model_id',e.target.value)}/></Toolbar>
    <QueryBoundary query={query} errorFallback="渠道大厅加载失败">{data=><><p className="muted-copy">共 {data.total} 个渠道</p>{data.items.length?<div className="sharing-grid">{data.items.map(c=><Card key={c.id} title={c.name}><Parents channel={c}/><p>{c.summary}</p><p>{c.models.length} 个模型</p><FormatTags formats={c.formats}/><ButtonLink variant="secondary" to={`/channel-hall/${c.id}`}>渠道详情</ButtonLink></Card>)}</div>:<EmptyState title="暂无符合条件的渠道"/>}<div className="form-actions"><Button variant="quiet" disabled={!params.get('cursor')} onClick={()=>update('cursor','')}>回到第一页</Button><Button variant="secondary" disabled={!data.next_cursor} onClick={()=>setParams(current=>{const n=new URLSearchParams(current);n.set('cursor',data.next_cursor??'');return n})}>下一页</Button></div></>}</QueryBoundary>
  </>
}
export function MarketHallChannelPage(){
  const {id=''}=useParams(),query=useMarketChannel(id)
  return <QueryBoundary query={query} errorFallback="渠道不存在或已下架">{c=><><PageHeader title={c.name} back={{to:'/channel-hall',label:'渠道大厅'}} actions={c.is_mine?<ButtonLink to={`/channels/${c.id}`}>管理渠道</ButtonLink>:undefined}/><Card><Parents channel={c}/><p>{c.summary}</p><MarkdownContent value={c.description}/><FormatTags formats={c.formats}/></Card><Card title="分组模型">{c.models.map(id=><div className="market-channel" key={id}><Link className="mono" to={`/models/${encodeURIComponent(id)}?group_id=${encodeURIComponent(c.group_id)}`}>{id}</Link></div>)}</Card></>}</QueryBoundary>
}
