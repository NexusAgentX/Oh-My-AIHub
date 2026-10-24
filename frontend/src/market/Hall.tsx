import { MarketQualitySection } from './Quality'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import type { components } from '../api/schema.gen'
import { FormatTags } from '../calls'
import { MarkdownContent } from '../markdown/MarkdownContent'
import { Badge, ButtonLink, Card, EmptyState, Icon, PageHeader, Pagination, QueryBoundary, SearchInput, TextField, Toolbar } from '../ui'
import { useMarketChannel, useMarketHall } from './queries'
import './Hall.css'

type Channel=components['schemas']['MarketHallChannel']
function Parents({channel}:{channel:Channel}){
  return <div className="sharing-line"><Link to={`/suppliers/${channel.supplier_id}`}>{channel.supplier_name}</Link><span aria-hidden="true">/</span><Link to={`/groups/${channel.group_id}`}>{channel.group_name}</Link>{channel.is_mine&&<Badge tone="accent">我的渠道</Badge>}</div>
}
function HallChannelCard({channel}:{channel:Channel}){
  return <Card className="hall-channel-card">
    <header className="hall-channel-heading">
      <h2><Link to={`/channel-hall/${channel.id}`}>{channel.name}</Link></h2>
      <Parents channel={channel}/>
    </header>
    {channel.summary&&<p className="hall-channel-summary">{channel.summary}</p>}
    <footer className="hall-channel-footer">
      <div className="hall-channel-meta"><span><strong className="num">{channel.models.length}</strong> 个模型</span><FormatTags formats={channel.formats}/></div>
      <ButtonLink variant="quiet" to={`/channel-hall/${channel.id}`}>查看详情<Icon name="chevron-right" size={16}/></ButtonLink>
    </footer>
  </Card>
}
export function MarketHallPage(){
  const [params,setParams]=useSearchParams(),query=useMarketHall(params)
  const update=(key:string,value:string)=>setParams(current=>{const next=new URLSearchParams(current);if(value)next.set(key,value);else next.delete(key);next.delete('cursor');return next},{replace:true})
  return <><PageHeader title="渠道大厅" actions={<ButtonLink to="/channels/new">共享渠道</ButtonLink>}/>
    <Toolbar><SearchInput label="搜索渠道" value={params.get('channel_q')??''} onChange={e=>update('channel_q',e.target.value)}/><TextField hideLabel label="模型 ID（精确）" placeholder="模型 ID（精确）" value={params.get('model_id')??''} onChange={e=>update('model_id',e.target.value)}/></Toolbar>
    <QueryBoundary query={query} errorFallback="渠道大厅加载失败">{data=><><p className="muted-copy">共 {data.total} 个渠道</p>{data.items.length?<div className="sharing-grid">{data.items.map(c=><HallChannelCard key={c.id} channel={c}/>)}</div>:<EmptyState title="暂无符合条件的渠道"/>}<Pagination hasPrevious={Boolean(params.get('cursor'))} onPrevious={()=>update('cursor','')} hasNext={Boolean(data.next_cursor)} onNext={()=>setParams(current=>{const n=new URLSearchParams(current);n.set('cursor',data.next_cursor??'');return n})}/></>}</QueryBoundary>
  </>
}
export function MarketHallChannelPage(){
  const {id=''}=useParams(),query=useMarketChannel(id)
  return <QueryBoundary query={query} errorFallback="渠道不存在或已下架">{c=><><PageHeader title={c.name} back={{to:'/channel-hall',label:'渠道大厅'}} actions={c.is_mine?<ButtonLink to={`/channels/${c.id}`}>管理渠道</ButtonLink>:undefined}/><Card><Parents channel={c}/><p>{c.summary}</p><MarkdownContent value={c.description}/><FormatTags formats={c.formats}/></Card><Card title="分组模型">{c.models.map(id=><div className="market-channel" key={id}><Link className="mono" to={`/models/${encodeURIComponent(id)}?group_id=${encodeURIComponent(c.group_id)}`}>{id}</Link></div>)}</Card><MarketQualitySection key={c.id} kind="channel" id={c.id}/></>}</QueryBoundary>
}
