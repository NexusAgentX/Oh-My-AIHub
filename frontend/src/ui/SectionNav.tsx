import { NavLink } from 'react-router-dom'

export function SectionNav({ label, items }: { label: string; items: { to: string; label: string; end?: boolean; dirty?: boolean }[] }) {
  return <nav className="section-nav" aria-label={label}>{items.map(item =>
    <NavLink key={item.to} to={item.to} end={item.end ?? true} className={({ isActive }) => `section-nav-link${isActive ? ' is-active' : ''}`}>
      {item.label}{item.dirty && <span aria-label="有未保存修改"> ●</span>}
    </NavLink>,
  )}</nav>
}
