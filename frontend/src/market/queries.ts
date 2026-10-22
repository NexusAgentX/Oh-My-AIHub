import { useMutation,useQuery,useQueryClient } from '@tanstack/react-query'
import { apiGet,apiSend,pathSegment,request } from '../api/client'
import type {ResponseBody,RoutingPreferenceInput} from '../api/types'

export const marketKeys={all:['market']as const,model:(id:string)=>['market','model',id]as const}

export function useMarketModels(params: URLSearchParams) {
  const search = params.toString()
  return useQuery({ queryKey: ['market', 'models', search], queryFn: () => apiGet<'listMarketModels'>(`/api/market/models?${search}`) })
}
export function useMarketModel(id: string, params: URLSearchParams) {
  const search = params.toString()
  return useQuery({ queryKey: ['market', 'model', id, search], queryFn: () => apiGet<'getMarketModel'>(`/api/market/models/${pathSegment(id)}?${search}`), enabled: Boolean(id) })
}
export function useMarketSuppliers() {
  return useQuery({ queryKey: ['market', 'suppliers'], queryFn: () => apiGet<'listMarketSuppliers'>('/api/market/suppliers') })
}
export function useMarketGroup(id: string) {
  return useQuery({ queryKey: ['market', 'group', id], queryFn: () => apiGet<'getMarketGroup'>(`/api/market/groups/${pathSegment(id)}`), enabled: Boolean(id) })
}
export function useMarketHall(params:URLSearchParams){
  const search=params.toString()
  return useQuery({queryKey:['market','hall',search],queryFn:()=>apiGet<'listMarketChannels'>(`/api/market/channels?${search}`)})
}
export function useMarketChannel(id:string){
  return useQuery({queryKey:['market','channel',id],queryFn:()=>apiGet<'getMarketChannel'>(`/api/market/channels/${pathSegment(id)}`),enabled:Boolean(id)})
}
/** Name-only consumers follow catalog pagination without silently truncating
 * the available-model picker. A changing snapshot fails and retries normally. */
export function useMarketNames(){
  return useQuery({queryKey:['market','names'],queryFn:async({signal})=>{
    const items:{id:string}[]=[],seen=new Set<string>()
    let cursor=''
    do{
      const params=new URLSearchParams({limit:'100'})
      if(cursor)params.set('cursor',cursor)
      const page=await request<ResponseBody<'listMarketModels'>>(`/api/market/models?${params}`,{signal})
      items.push(...page.items.map(({id})=>({id})))
      cursor=page.next_cursor??''
      if(cursor&&seen.has(cursor))throw new Error('目录分页已变化，请重试')
      seen.add(cursor)
    }while(cursor)
    return {items}
  }})
}
export function useSetMarketRouting(id:string){
  const client=useQueryClient()
  return useMutation({mutationFn:(body:RoutingPreferenceInput)=>apiSend<'setRouting'>('PUT',`/api/routing/${pathSegment(id)}`,body),onSuccess:()=>client.invalidateQueries({queryKey:marketKeys.model(id)})})
}

/** Supplier quality filters need every visible channel name, across pages. */
export async function loadMarketChannelNames(supplierID: string, signal?: AbortSignal) {
  const items: { id: string; name: string }[] = [], seen = new Set<string>()
  let cursor = ''
  do {
    const params = new URLSearchParams({ supplier_id: supplierID, limit: '100' })
    if (cursor) params.set('cursor', cursor)
    const page = await request<ResponseBody<'listMarketChannels'>>(`/api/market/channels?${params}`, { signal })
    items.push(...page.items.map(({ id, name }) => ({ id, name })))
    cursor = page.next_cursor ?? ''
    if (cursor && seen.has(cursor)) throw new Error('渠道目录分页已变化，请重试')
    seen.add(cursor)
  } while (cursor)
  return items
}
export function useMarketChannelNames(supplierID: string) {
  return useQuery({ queryKey: ['market', 'channel-names', supplierID], queryFn: ({ signal }) => loadMarketChannelNames(supplierID, signal) })
}
