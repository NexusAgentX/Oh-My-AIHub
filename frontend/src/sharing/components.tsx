import type { components } from '../api/schema.gen'
import './sharing.css'

type Supplier = components['schemas']['MarketBrowseSupplier']

export function SupplierIcon({ supplier }: { supplier?: Pick<Supplier, 'name' | 'icon_data'> | null }) {
  return supplier?.icon_data ? <img className="supplier-icon" src={supplier.icon_data} alt="" width={44} height={44} /> : <span aria-hidden="true" className="supplier-icon supplier-initial">{supplier?.name.slice(0, 1) || '渠'}</span>
}
