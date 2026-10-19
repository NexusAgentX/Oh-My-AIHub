import type { ReactNode } from 'react'
import './Facts.css'

export function Facts({ items }: { items: { label: string; value?: ReactNode }[] }) {
  return <dl className="facts">{items.filter(item => item.value !== null && item.value !== undefined && item.value !== '' && item.value !== false).map(item => <div key={item.label}><dt>{item.label}</dt><dd>{item.value}</dd></div>)}</dl>
}
