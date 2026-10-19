import type { ReactNode } from 'react'
import './Facts.css'

export function Facts({ items }: { items: readonly [label: string, value?: ReactNode][] }) {
  return <dl className="facts">{items.filter(([, value]) => value !== null && value !== undefined && value !== '' && value !== false).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
}
