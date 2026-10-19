import { useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { SelectField } from '../ui'

export function WorkspaceSwitcher() {
  const { account } = useAuth()
  const { pathname } = useLocation()
  const navigate = useNavigate()
  if (!account?.is_admin) return null
  return <SelectField className="workspace-switcher" label="工作区" value={pathname.startsWith('/admin') ? 'admin' : 'member'}
    onChange={event => navigate(event.target.value === 'admin' ? '/admin' : '/home')}>
    <option value="member">成员</option><option value="admin">治理</option>
  </SelectField>
}
