import { NavLink } from 'react-router-dom'
import type { components } from '../api/schema.gen'
import './sharing.css'

type Supplier = components['schemas']['MarketBrowseSupplier']

export function SharingNav() {
  return <nav className="sharing-nav" aria-label="共享栏目">
    <NavLink to="/channel-hall">渠道大厅</NavLink><NavLink to="/suppliers">供应商</NavLink><NavLink to="/channels">我的渠道</NavLink>
  </nav>
}
export function SupplierIcon({ supplier }: { supplier?: Pick<Supplier, 'name' | 'icon_data'> | null }) {
  return supplier?.icon_data ? <img className="supplier-icon" src={supplier.icon_data} alt="" width={44} height={44} /> : <span aria-hidden="true" className="supplier-icon supplier-initial">{supplier?.name.slice(0, 1) || '渠'}</span>
}
