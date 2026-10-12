import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiSend } from '../api/client'
import { SupplierIcon } from './components'
import { Link } from 'react-router-dom'

export function SupplierRecognition({ baseURL }: { baseURL: string }) {
  const [address, setAddress] = useState('')
  useEffect(() => { const timer = setTimeout(() => setAddress(baseURL.trim()), 400); return () => clearTimeout(timer) }, [baseURL])
  const query = useQuery({ queryKey: ['sharing', 'recognition', address], enabled: address.startsWith('https://'), retry: false,
    queryFn: () => apiSend<'recognizeSupplier'>('POST', '/api/suppliers/recognize', { base_url: address }) })
  if (!address.startsWith('https://') || address !== baseURL.trim()) return null
  return <div className="supplier-preview"><SupplierIcon supplier={query.data?.supplier} />{query.isPending ? '正在识别供应商…' : query.isError ? '暂时无法识别供应商' : query.data?.supplier ? <Link to={`/suppliers/${query.data.supplier.id}`}>{query.data.supplier.name} · 已识别</Link> : '未识别供应商'}</div>
}
